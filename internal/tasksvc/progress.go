package tasksvc

import (
	"context"
	"time"
)

type Reporter struct {
	manager *Manager
	run     *execution
	ctx     context.Context
}

func (r *Reporter) Report(progress Progress) error {
	r.run.mu.Lock()
	defer r.run.mu.Unlock()
	if r.run.task.Status != Running {
		return ErrClosed
	}
	if progress.Completed < 0 || (progress.Total != nil && (*progress.Total < 0 || progress.Completed > *progress.Total)) ||
		len(progress.Phase) > 128 || len(progress.Unit) > 64 || len(progress.Counters) > 32 ||
		(progress.Phase == r.run.task.Progress.Phase && progress.Completed < r.run.task.Progress.Completed) {
		r.run.err = ErrProgress
		return ErrProgress
	}
	for key, value := range progress.Counters {
		if len(key) > 64 || value < 0 {
			r.run.err = ErrProgress
			return ErrProgress
		}
	}
	previousPhase := r.run.task.Progress.Phase
	r.run.task.Progress = cloneProgress(progress)
	r.run.task.UpdatedAt = time.Now().UnixMilli()
	// Persist at most four updates per second, except phase boundaries and
	// completed totals. The terminal receipt always saves the latest progress.
	if previousPhase != progress.Phase || time.Since(r.run.lastPersist) >= 250*time.Millisecond ||
		(progress.Total != nil && progress.Completed == *progress.Total) {
		if err := r.manager.store.save(r.ctx, r.run.task); err != nil {
			r.run.err = err
			return err
		}
		r.run.lastPersist = time.Now()
	}
	return nil
}

func cloneProgress(progress Progress) Progress {
	if progress.Total != nil {
		total := *progress.Total
		progress.Total = &total
	}
	counters := make(map[string]int64, len(progress.Counters))
	for key, value := range progress.Counters {
		counters[key] = value
	}
	progress.Counters = counters
	return progress
}

func cloneTask(task Task) Task { task.Progress = cloneProgress(task.Progress); return task }
