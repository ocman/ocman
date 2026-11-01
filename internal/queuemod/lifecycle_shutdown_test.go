package queuemod

import (
	"context"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

type blockedLifecycle struct {
	lifecyclePlatform
	entered, release, cancelled chan struct{}
	t                           *testing.T
}

func (p blockedLifecycle) SessionLifecycle(ctx context.Context, _ string) (*platforms.SessionLifecycle, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.t.Error("lifecycle read has no deadline")
	}
	close(p.entered)
	select {
	case <-ctx.Done():
		close(p.cancelled)
		return nil, ctx.Err()
	case <-p.release:
		return p.lifecyclePlatform.SessionLifecycle(ctx, "")
	}
}

func TestShutdownCancelsPreSendLifecycleRead(t *testing.T) {
	for _, path := range []string{"sweep", "idle"} {
		t.Run(path, func(t *testing.T) {
			store := statetest.Open(t)
			p := blockedLifecycle{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{}), t: t}
			sent := make(chan struct{}, 1)
			m := NewModule(Deps{
				Store: store, Sessions: func() Sessions { return nil },
				Adapter:   func(context.Context, string, string) (platforms.Platform, bool) { return p, true },
				SendNow:   func(context.Context, string, platforms.SendMessageRequest) error { sent <- struct{}{}; return nil },
				Broadcast: func(string, []byte) {},
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if err := m.Start(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Service().Enqueue(t.Context(), "r-owner:opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "held", Agent: "plan", Model: "p/m"}); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			if path == "idle" {
				m.OnSessionIdle("r-owner:opencode", "s1")
				<-p.entered
				cancel()
				go func() { m.Run(ctx); close(done) }()
			} else {
				go func() { m.Run(ctx); close(done) }()
				<-p.entered
				cancel()
			}
			defer func() { close(p.release); <-done }()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("shutdown did not cancel and join the pre-send lifecycle read")
			}
			select {
			case <-p.cancelled:
			default:
				t.Error("lifecycle read was not cancelled")
			}
			select {
			case <-sent:
				t.Error("cancelled pre-send read delivered a prompt")
			default:
			}
			if rows, err := m.Service().List(t.Context(), "r-owner:opencode", "s1"); err != nil || len(rows) != 1 || rows[0].Text != "held" || rows[0].Model != "p/m" || rows[0].Agent != "plan" {
				t.Fatalf("payload changed: %+v %v", rows, err)
			}
		})
	}
}

// TestShutdownCancelsAndJoinsRetryCooldownRead is a regression test: the
// retry edge read state.db and the owner on context.Background(), so a
// slow read outlived Run and the database it reads.
func TestShutdownCancelsAndJoinsRetryCooldownRead(t *testing.T) {
	store := statetest.Open(t)
	p := blockedOwner{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{}), returned: make(chan struct{}), t: t}
	m := NewModule(Deps{
		Store: store, Sessions: func() Sessions { return nil },
		Adapter:   func(context.Context, string, string) (platforms.Platform, bool) { return p, true },
		SendNow:   func(context.Context, string, platforms.SendMessageRequest) error { return nil },
		Broadcast: func(string, []byte) {},
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	retryDone, runDone := make(chan struct{}), make(chan struct{})
	go func() { m.OnSessionRetry("r-owner:opencode", "s1", 0, "account_rate_limit"); close(retryDone) }()
	<-p.entered
	cancel()
	go func() { m.Run(ctx); close(runDone) }()
	defer func() { close(p.release); <-retryDone; <-runDone }()
	select {
	case <-p.cancelled:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel the retry cooldown read")
	}
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after the retry read was cancelled")
	}
	// Run waited for the handler: the owner read inside it has returned. The
	// caller's retryDone closes after the handler's deferred Done, so it
	// gets a bounded wait rather than a non-blocking check.
	select {
	case <-p.returned:
	default:
		t.Fatal("Run returned before joining the retry handler")
	}
	select {
	case <-retryDone:
	case <-time.After(time.Second):
		t.Fatal("retry handler did not return")
	}
}

// TestRetryAfterRunTouchesNothing checks that a retry edge arriving after
// Run returned does not reach the owner or state.db.
func TestRetryAfterRunTouchesNothing(t *testing.T) {
	store := statetest.Open(t)
	adapterCalls := 0
	m := NewModule(Deps{
		Store: store, Sessions: func() Sessions { return nil },
		Adapter: func(context.Context, string, string) (platforms.Platform, bool) {
			adapterCalls++
			return lifecyclePlatform{}, true
		},
		SendNow:   func(context.Context, string, platforms.SendMessageRequest) error { return nil },
		Broadcast: func(string, []byte) {},
	})
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	m.Run(ctx)
	m.OnSessionRetry("r-owner:opencode", "s1", 0, "account_rate_limit")
	if adapterCalls != 0 {
		t.Fatalf("retry after Run resolved the owner %d times", adapterCalls)
	}
}
