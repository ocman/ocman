package platformconnections

import (
	"context"
	"fmt"
	"sync"
)

type connectionLifecycle struct {
	mu      sync.Mutex
	entries map[string]*connectionLease
}

type connectionLease struct {
	fenced   bool
	draining bool
	active   int
	idle     chan struct{}
}

func (l *connectionLifecycle) entry(id string) *connectionLease {
	if l.entries == nil {
		l.entries = make(map[string]*connectionLease)
	}
	e := l.entries[id]
	if e == nil {
		e = &connectionLease{idle: make(chan struct{})}
		close(e.idle)
		l.entries[id] = e
	}
	return e
}

// Begin leases one mutation through native effects and durable receipts. Prompt
// answers do not use this gate, so draining work can still finish.
func (r *Router) Begin(ctx context.Context, id string) (func(), error) {
	r.lifecycle.mu.Lock()
	e := r.lifecycle.entry(id)
	if e.fenced {
		r.lifecycle.mu.Unlock()
		return nil, fmt.Errorf("%w: connection is draining", ErrUnavailable)
	}
	if e.active == 0 {
		e.idle = make(chan struct{})
	}
	e.active++
	r.lifecycle.mu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			r.lifecycle.mu.Lock()
			defer r.lifecycle.mu.Unlock()
			e.active--
			if e.active == 0 {
				close(e.idle)
			}
		})
	}
	route, err := r.Resolve(ctx, Reference{ConnectionID: id})
	if err != nil {
		release()
		return nil, err
	}
	if ready, ok := route.Platform.(interface{ ExecutionReady(context.Context) error }); ok {
		if err := ready.ExecutionReady(ctx); err != nil {
			release()
			return nil, fmt.Errorf("%w: native account is not ready: %w", ErrUnavailable, err)
		}
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// Detach fences new mutations and drains admitted work before changing intent.
// Cancellation or failed persistence restores the gate; success retains it.
func (r *Router) Detach(ctx context.Context, id string, remove func() error) error {
	if remove == nil || r.store == nil {
		return ErrInvalid
	}
	if _, err := r.store.Get(ctx, id); err != nil {
		return err
	}
	r.lifecycle.mu.Lock()
	e := r.lifecycle.entry(id)
	if e.draining {
		r.lifecycle.mu.Unlock()
		return fmt.Errorf("%w: connection is already draining", ErrUnavailable)
	}
	previousFence := e.fenced
	e.fenced = true
	e.draining = true
	idle := e.idle
	r.lifecycle.mu.Unlock()
	restore := func() {
		r.lifecycle.mu.Lock()
		e.fenced = previousFence
		e.draining = false
		r.lifecycle.mu.Unlock()
	}
	select {
	case <-ctx.Done():
		restore()
		return ctx.Err()
	case <-idle:
	}
	if err := ctx.Err(); err != nil {
		restore()
		return err
	}
	if err := remove(); err != nil {
		restore()
		return err
	}
	r.lifecycle.mu.Lock()
	e.draining = false
	r.lifecycle.mu.Unlock()
	return nil
}
