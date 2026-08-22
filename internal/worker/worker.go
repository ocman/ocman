// Package worker runs queued work serially and exposes deterministic draining.
package worker

import (
	"context"
	"sync"
)

type Worker[T any] struct {
	mu          sync.Mutex
	handle      func(T)
	key         func(T) string
	queue       []T
	active      map[string]bool
	outstanding int
	wake        chan struct{}
	idle        chan struct{}
}

func New[T any](handle func(T)) *Worker[T] {
	return NewKeyed(handle, func(T) string { return "" })
}

// NewKeyed runs different keys independently while preserving serial order
// within each key. Drain still waits for every queued and active item.
func NewKeyed[T any](handle func(T), key func(T) string) *Worker[T] {
	idle := make(chan struct{})
	close(idle)
	w := &Worker[T]{handle: handle, key: key, active: make(map[string]bool), wake: make(chan struct{}, 1), idle: idle}
	go w.run()
	return w
}

func (w *Worker[T]) Enqueue(v T) {
	w.mu.Lock()
	if w.outstanding == 0 {
		w.idle = make(chan struct{})
	}
	w.queue = append(w.queue, v)
	w.outstanding++
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker[T]) Drain(ctx context.Context) error {
	w.mu.Lock()
	idle := w.idle
	w.mu.Unlock()
	select {
	case <-idle:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker[T]) run() {
	for range w.wake {
		for {
			w.mu.Lock()
			i := -1
			for candidate, v := range w.queue {
				if !w.active[w.key(v)] {
					i = candidate
					break
				}
			}
			if i < 0 {
				w.mu.Unlock()
				break
			}
			v := w.queue[i]
			key := w.key(v)
			var zero T
			copy(w.queue[i:], w.queue[i+1:])
			w.queue[len(w.queue)-1] = zero
			w.queue = w.queue[:len(w.queue)-1]
			w.active[key] = true
			w.mu.Unlock()

			go w.handleOne(key, v)
		}
	}
}

func (w *Worker[T]) handleOne(key string, v T) {
	w.handle(v)

	w.mu.Lock()
	delete(w.active, key)
	w.outstanding--
	if w.outstanding == 0 {
		close(w.idle)
	}
	w.mu.Unlock()

	select {
	case w.wake <- struct{}{}:
	default:
	}
}
