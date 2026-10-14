package module

import (
	"context"
	"runtime/debug"
	"sync"

	log "github.com/sirupsen/logrus"
)

// SessionIdle is a session's turn ending: the agent is waiting for input.
type SessionIdle struct {
	// Platform is the compound platform id that owns the session.
	Platform  string
	SessionID string
}

// FirstPromptAdded is the first successfully accepted input in a session.
type FirstPromptAdded struct {
	Platform, SessionID, Directory, Prompt string
	HasCustomTitle                         bool
}

// Bus delivers typed server events to the modules that subscribed.
//
// Delivery is synchronous, in subscription order, so a subscriber can rely
// on the ones before it having run; one that does slow work starts its own
// goroutine. A panicking subscriber is logged and the rest still run.
// ponytail: one slice per event type; generic Topic[T] once there are ~6.
type Bus struct {
	mu          sync.RWMutex
	idle        []func(context.Context, SessionIdle)
	firstPrompt []func(context.Context, FirstPromptAdded)
}

// Subscriber is a module that reacts to server events.
type Subscriber interface {
	Subscribe(*Bus)
}

func (b *Bus) OnSessionIdle(f func(context.Context, SessionIdle)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.idle = append(b.idle, f)
}

func (b *Bus) PublishSessionIdle(ctx context.Context, e SessionIdle) {
	b.mu.RLock()
	subs := b.idle
	b.mu.RUnlock()
	for _, f := range subs {
		deliver(func() { f(ctx, e) })
	}
}

func (b *Bus) OnFirstPromptAdded(f func(context.Context, FirstPromptAdded)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.firstPrompt = append(b.firstPrompt, f)
}

func (b *Bus) PublishFirstPromptAdded(ctx context.Context, e FirstPromptAdded) {
	b.mu.RLock()
	subs := b.firstPrompt
	b.mu.RUnlock()
	for _, f := range subs {
		deliver(func() { f(ctx, e) })
	}
}

func deliver(f func()) {
	defer func() {
		if r := recover(); r != nil {
			log.WithFields(log.Fields{"panic": r, "stack": string(debug.Stack())}).Error("event subscriber panicked")
		}
	}()
	f()
}
