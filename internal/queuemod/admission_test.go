package queuemod

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type blockedOwner struct {
	lifecyclePlatform
	entered   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
	t         *testing.T
}

func (p blockedOwner) Session(ctx context.Context, _ string, _, _ int) (*platforms.SessionDetail, error) {
	if _, ok := ctx.Deadline(); !ok {
		p.t.Error("cooldown read has no deadline")
	}
	close(p.entered)
	select {
	case <-ctx.Done():
		close(p.cancelled)
		return nil, ctx.Err()
	case <-p.release:
		return &platforms.SessionDetail{}, nil
	}
}

func TestIdleAdmissionDoesNotBlockOtherOwner(t *testing.T) {
	store := statetest.Open(t)
	p := blockedOwner{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{}), t: t}
	sent := make(chan string, 2)
	m := NewModule(Deps{
		Store: store, Sessions: func() Sessions { return nil },
		Adapter: func(_ context.Context, owner, _ string) (platforms.Platform, bool) {
			if owner == "r-blocked:opencode" {
				return p, true
			}
			return lifecyclePlatform{}, true
		},
		SendNow:   func(_ context.Context, owner string, _ platforms.SendMessageRequest) error { sent <- owner; return nil },
		Broadcast: func(string, []byte) {},
	})
	for _, owner := range []string{"r-blocked:opencode", "claudecode"} {
		if err := m.Service().Enqueue(t.Context(), owner, true, platforms.SendMessageRequest{SessionID: "same", Message: "held"}); err != nil {
			t.Fatal(err)
		}
	}
	blockedDone, otherDone := make(chan struct{}), make(chan struct{})
	go func() { m.OnSessionIdle("r-blocked:opencode", "same"); close(blockedDone) }()
	<-p.entered
	go func() { m.OnSessionIdle("claudecode", "same"); close(otherDone) }()
	defer func() {
		close(p.release)
		<-blockedDone
		<-otherDone
		if err := m.Drain(t.Context()); err != nil {
			t.Error(err)
		}
	}()
	select {
	case owner := <-sent:
		if owner != "claudecode" {
			t.Fatal(owner)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked cooldown read prevented another owner's idle flush")
	}
}

func TestShutdownCancelsAndJoinsIdleCooldownRead(t *testing.T) {
	store := statetest.Open(t)
	p := blockedOwner{entered: make(chan struct{}), release: make(chan struct{}), cancelled: make(chan struct{}), t: t}
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
	if err := m.Service().Enqueue(t.Context(), "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "held"}); err != nil {
		t.Fatal(err)
	}
	idleDone, runDone := make(chan struct{}), make(chan struct{})
	go func() { m.OnSessionIdle("opencode", "s1"); close(idleDone) }()
	<-p.entered
	go func() { m.Run(ctx); close(runDone) }()
	cancel()
	defer func() { close(p.release); <-idleDone; <-runDone }()
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel and join the blocked cooldown read")
	}
	select {
	case <-p.cancelled:
	default:
		t.Error("owner read was not cancelled")
	}
	select {
	case <-sent:
		t.Error("shutdown sent a prompt whose cooldown read was cancelled")
	default:
	}
	if rows, err := m.Service().List(t.Context(), "opencode", "s1"); err != nil || len(rows) != 1 || rows[0].Text != "held" {
		t.Fatalf("held payload lost: %+v %v", rows, err)
	}
}
