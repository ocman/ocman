package tasksvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

// ObserveSchedule records one latest receipt per recurring job. It executes
// synchronously outside the bounded user-work queue, so maintenance cannot
// starve or deadlock the work it is monitoring. The caller owns cancellation
// and must cancel its lifetime before Stop, which joins admitted observations.
// Descendant subscriptions retain that lifetime after an iteration finishes.
func (m *Manager) ObserveSchedule(caller context.Context, spec module.Schedule, work func(context.Context) error) error {
	if work == nil || spec.Name == "" || len(spec.Name) > 119 || spec.Title == "" || len(spec.Title) > 256 || len(spec.Scope) > 512 || spec.Interval <= 0 {
		return fmt.Errorf("invalid scheduled task specification")
	}
	if !m.Enabled() {
		return work(caller)
	}
	m.mu.Lock()
	if m.closed || m.ctx == nil || m.ctx.Err() != nil {
		m.mu.Unlock()
		return ErrClosed
	}
	if err := caller.Err(); err != nil {
		m.mu.Unlock()
		return err
	}
	if m.schedules[spec.Name] {
		m.mu.Unlock()
		return ErrBusy
	}
	now := time.Now().UnixMilli()
	identity := sha256.Sum256([]byte("ocman.schedule." + spec.Name))
	task := Task{ID: hex.EncodeToString(identity[:]), Spec: Spec{Kind: "schedule." + spec.Name, Key: spec.Name, Title: spec.Title, Scope: "local"}, OwnerID: "local", Status: Running, CreatedAt: now, StartedAt: now, UpdatedAt: now,
		Progress: Progress{Phase: "starting", Counters: map[string]int64{}}, Schedule: &ScheduleInfo{IntervalMillis: spec.Interval.Milliseconds(), Runs: 1}}
	if spec.Scope != "" {
		task.Scope = spec.Scope
	}
	previous, err := m.Get(caller, task.ID)
	if err == nil {
		task.CreatedAt = previous.CreatedAt
		if previous.Schedule != nil {
			task.Schedule.Runs += previous.Schedule.Runs
		}
	}
	if errors.Is(err, ErrNotFound) {
		err = nil
	}
	if err == nil {
		err = m.store.beginScheduled(caller, task)
	}
	if m.schedules == nil {
		m.schedules = make(map[string]bool)
	}
	m.schedules[spec.Name] = true
	m.observed.Add(1)
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.schedules, spec.Name)
		m.mu.Unlock()
		m.observed.Done()
	}()
	if err != nil {
		log.WithError(err).WithField("job", spec.Name).Warn("scheduled task receipt unavailable; running maintenance without observation")
		return runScheduledWork(caller, work)
	}
	ctx := caller
	run := &execution{task: task}
	reporter := &Reporter{manager: m, run: run, ctx: ctx}
	ctx = module.WithProgressReporter(ctx, reporter)
	workErr := runScheduledWork(ctx, work)
	m.finish(run, workErr, ctx.Err())
	return run.err
}

func runScheduledWork(ctx context.Context, work func(context.Context) error) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("scheduled task panicked")
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return work(ctx)
}

func (m *Manager) StopSchedule(ctx context.Context, name string) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.observed.Add(1)
	m.mu.Unlock()
	defer m.observed.Done()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_, err := m.store.db.ExecContext(ctx, `UPDATE background_task SET data=json_set(data,'$.schedule.nextRunAt',0) WHERE kind=? AND task_key=?`, "schedule."+name, name)
	return err
}

// MaintenanceBlockers counts admitted background executions, not inline
// scheduler observations. Observing an existing loop must not change its
// admission rules: real work still uses the domain's mutation fences.
func (m *Manager) MaintenanceBlockers(ctx context.Context) (int, error) {
	if !m.Enabled() {
		return 0, ErrUnavailable
	}
	var count int
	err := m.store.db.QueryRowContext(ctx, `SELECT count(*) FROM background_task WHERE status IN ('queued','running')
		AND COALESCE(json_type(data,'$.schedule'),'null')<>'object'`).Scan(&count)
	return count, err
}
