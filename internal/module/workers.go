package module

import (
	"context"
	"sync"
)

// Workers runs a module's ad-hoc goroutines (typically spawned from a Bus
// subscriber) under its lifetime context, so Close can join them before
// state.db closes. The zero value is ready; unbound, work runs on
// context.Background().
type Workers struct {
	mu      sync.Mutex
	ctx     context.Context
	stopped bool
	wg      sync.WaitGroup
}

// Bind sets the lifetime context later work receives.
func (w *Workers) Bind(ctx context.Context) {
	w.mu.Lock()
	w.ctx = ctx
	w.mu.Unlock()
}

// Go runs fn with the lifetime context, unless Close has begun.
func (w *Workers) Go(fn func(context.Context)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return false
	}
	ctx := w.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	w.wg.Go(func() { fn(ctx) })
	return true
}

// Close refuses new work and waits for running work.
func (w *Workers) Close() {
	w.mu.Lock()
	w.stopped = true
	w.mu.Unlock()
	w.wg.Wait()
}
