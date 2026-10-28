package tasksvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
)

type Manager struct {
	store   store
	mu      sync.Mutex
	ctx     context.Context
	cancel  context.CancelFunc
	closed  bool
	pending int
	workers module.Workers
	slots   chan struct{}
}

func New(database *state.DB, concurrency int) *Manager {
	if concurrency < 1 {
		concurrency = 4
	}
	m := &Manager{slots: make(chan struct{}, concurrency)}
	if database != nil {
		m.store.db = database.SQL()
	}
	return m
}

func (m *Manager) Enabled() bool { return m != nil && m.store.db != nil }

// Start interrupts stale records; arbitrary queued work is never replayed.
func (m *Manager) Start(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrClosed
	}
	if m.ctx != nil {
		return nil
	}
	if err := m.store.recover(ctx); err != nil {
		return err
	}
	if err := m.store.prune(ctx); err != nil {
		return err
	}
	m.ctx, m.cancel = context.WithCancel(ctx)
	m.workers.Bind(m.ctx)
	return nil
}

func (m *Manager) Stop() {
	m.mu.Lock()
	m.closed = true
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()
	m.workers.Close()
}

type execution struct {
	mu          sync.Mutex
	task        Task
	err         error
	done        chan struct{}
	lastPersist time.Time
}

func (m *Manager) submit(ctx context.Context, spec Spec, work Work) (*execution, error) {
	if !m.Enabled() {
		return nil, ErrUnavailable
	}
	if work == nil || spec.Kind == "" || spec.Title == "" || len(spec.Kind) > 128 || len(spec.Key) > 256 || len(spec.Title) > 256 || len(spec.Scope) > 512 {
		return nil, fmt.Errorf("invalid background task specification")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.ctx == nil || m.ctx.Err() != nil {
		return nil, ErrClosed
	}
	if m.pending >= 128 {
		return nil, ErrBusy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now().UnixMilli()
	run := &execution{done: make(chan struct{}), task: Task{ID: uuid.NewString(), Spec: spec, OwnerID: "local", Status: Queued,
		CreatedAt: now, UpdatedAt: now, Progress: Progress{Phase: "queued", Counters: map[string]int64{}}}}
	if err := m.store.insert(ctx, run.task); err != nil {
		return nil, err
	}
	m.pending++
	m.workers.Go(func(lifetime context.Context) {
		defer func() { m.mu.Lock(); m.pending--; m.mu.Unlock() }()
		m.execute(lifetime, ctx, run, work)
	})
	return run, nil
}

// Submit accepts work durably before scheduling it. Only in-process callers
// can submit typed closures; there is no arbitrary-command HTTP endpoint.
func (m *Manager) Submit(ctx context.Context, spec Spec, work Work) (Task, error) {
	run, err := m.submit(ctx, spec, work)
	if err != nil {
		return Task{}, err
	}
	run.mu.Lock()
	defer run.mu.Unlock()
	return cloneTask(run.task), nil
}

// Do runs background work and waits for its final receipt. Cancellation still
// joins admitted work, rather than letting it outlive the caller's databases.
func (m *Manager) Do(ctx context.Context, spec Spec, work Work) error {
	run, err := m.submit(ctx, spec, work)
	if err != nil {
		return err
	}
	<-run.done
	return run.err
}

func (m *Manager) execute(lifetime, caller context.Context, run *execution, work Work) {
	ctx, cancel := context.WithCancel(lifetime)
	stop := context.AfterFunc(caller, cancel)
	if caller.Err() != nil {
		cancel()
	}
	defer cancel()
	defer stop()
	defer close(run.done)
	var workErr error
	defer func() {
		if recovered := recover(); recovered != nil {
			// Do not retain panic values, which can contain arbitrary task input.
			workErr = errors.New("background task panicked")
		}
		m.finish(run, workErr, ctx.Err())
	}()
	select {
	case m.slots <- struct{}{}:
		defer func() { <-m.slots }()
	case <-ctx.Done():
		workErr = ctx.Err()
		return
	}
	if err := ctx.Err(); err != nil {
		workErr = err
		return
	}
	run.mu.Lock()
	run.task.Status = Running
	run.task.StartedAt = time.Now().UnixMilli()
	run.task.UpdatedAt = run.task.StartedAt
	run.task.Progress.Phase = "starting"
	workErr = m.store.save(ctx, run.task)
	run.mu.Unlock()
	if workErr == nil {
		workErr = work(ctx, &Reporter{manager: m, run: run, ctx: ctx})
	}
}

func (m *Manager) finish(run *execution, workErr, ctxErr error) {
	run.mu.Lock()
	defer run.mu.Unlock()
	err := errors.Join(workErr, run.err)
	run.task.Status = Succeeded
	if err != nil {
		run.task.Status = Failed
	}
	if ctxErr != nil {
		run.task.Status = Cancelled
		err = errors.Join(err, ctxErr)
	}
	if err == nil && run.task.Progress.Total != nil && run.task.Progress.Completed < *run.task.Progress.Total {
		err = errors.New("task returned before completing its reported work")
		run.task.Status = Failed
	}
	if err != nil {
		run.task.Error = truncate(err.Error(), 1024)
	} else {
		run.task.Progress.Phase = "finished"
	}
	run.task.FinishedAt = time.Now().UnixMilli()
	run.task.UpdatedAt = run.task.FinishedAt
	// Only the final receipt detaches cancellation. Stop joins it before the
	// caller closes state.db; arbitrary task execution never detaches here.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = errors.Join(err, m.store.save(ctx, run.task))
	if pruneErr := m.store.prune(ctx); pruneErr != nil {
		err = errors.Join(err, pruneErr)
	}
	run.err = err
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return strings.ToValidUTF8(value[:limit], "")
}
