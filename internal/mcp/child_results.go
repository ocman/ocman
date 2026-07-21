package mcp

import (
	"context"
	"fmt"
	"sync"
)

// ChildResult is the terminal result returned by a child session.
type ChildResult struct {
	Status  string
	Summary string
}

// ChildResultBroker connects a synchronous new_session call to the
// background child-session watcher.
type ChildResultBroker struct {
	mu      sync.Mutex
	waiters map[string]chan ChildResult
}

func NewChildResultBroker() *ChildResultBroker {
	return &ChildResultBroker{waiters: make(map[string]chan ChildResult)}
}

// Register marks a child result for delivery through its MCP call. It refuses
// to replace an existing waiter.
func (b *ChildResultBroker) Register(childID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.waiters[childID]; exists {
		return false
	}
	b.waiters[childID] = make(chan ChildResult, 1)
	return true
}

func (b *ChildResultBroker) Unregister(childID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiters, childID)
}

func (b *ChildResultBroker) Registered(childID string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.waiters[childID]
	return ok
}

// Deliver sends a result to a waiting MCP call. False means no synchronous
// caller owns the result.
func (b *ChildResultBroker) Deliver(childID string, result ChildResult) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.waiters[childID]
	if !ok {
		return false
	}
	select {
	case ch <- result:
	default:
	}
	return true
}

// Wait blocks until the watcher delivers the child's terminal result or the
// MCP request is cancelled.
func (b *ChildResultBroker) Wait(ctx context.Context, childID string) (ChildResult, error) {
	return b.wait(ctx, childID, true)
}

func (b *ChildResultBroker) WaitOwned(ctx context.Context, childID string) (ChildResult, error) {
	return b.wait(ctx, childID, false)
}

func (b *ChildResultBroker) wait(ctx context.Context, childID string, remove bool) (ChildResult, error) {
	b.mu.Lock()
	ch, ok := b.waiters[childID]
	b.mu.Unlock()
	if !ok {
		return ChildResult{}, fmt.Errorf("child session %s is not registered", childID)
	}
	if err := ctx.Err(); err != nil {
		if remove {
			b.remove(childID, ch)
		}
		return ChildResult{}, err
	}

	select {
	case result := <-ch:
		if remove {
			b.remove(childID, ch)
		}
		if err := ctx.Err(); err != nil {
			return ChildResult{}, err
		}
		return result, nil
	case <-ctx.Done():
		if remove {
			b.remove(childID, ch)
		}
		return ChildResult{}, ctx.Err()
	}
}

func (b *ChildResultBroker) remove(childID string, ch chan ChildResult) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.waiters[childID] == ch {
		delete(b.waiters, childID)
	}
}
