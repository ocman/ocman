package startup

import (
	"context"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

func TestProgressLogsCountersAndDoesNotLeakBetweenContexts(t *testing.T) {
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	ctx, p := New(t.Context())
	p.started = time.Now().Add(-10 * time.Second)
	Report(context.Background(), "not startup", 10, 10)
	Report(ctx, "Rekeying session references", 1, 10)
	Report(ctx, "Rekeying session references", 2, 10)
	if len(hook.AllEntries()) != 1 {
		t.Fatal("progress logs not throttled")
	}
	p.logged = time.Now().Add(-3 * time.Second)
	Report(ctx, "Rekeying session references", 3, 10)
	Report(ctx, "Rekeying session references", 10, 10)
	if len(hook.AllEntries()) != 3 {
		t.Fatal("timed update or completion log missing")
	}
	s := p.Snapshot()
	if s.State != "starting" || s.Completed != 10 || s.Total != 10 || s.Elapsed < 10 {
		t.Fatalf("progress = %+v", s)
	}
	_, other := New(t.Context())
	if other.Snapshot().Completed != 0 {
		t.Fatal("progress shared across startups")
	}
	p.finish("ready", "Ready")
	if p.Snapshot().State != "ready" || hook.LastEntry().Data["state"] != "ready" {
		t.Fatal("readiness not logged")
	}
}

func TestProgressHeartbeatWhileStatementIsBusy(t *testing.T) {
	hook := logtest.NewLocal(log.StandardLogger())
	t.Cleanup(func() { log.StandardLogger().ReplaceHooks(make(log.LevelHooks)) })
	ctx, cancel := context.WithCancel(t.Context())
	ctx, p := New(ctx)
	done := make(chan struct{})
	go func() { defer close(done); p.Heartbeat(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	for deadline := time.Now().Add(7 * time.Second); len(hook.AllEntries()) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("no heartbeat while SQL is busy")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if hook.LastEntry().Message != "startup progress" {
		t.Fatal("unexpected heartbeat")
	}
	p.finish("ready", "Ready")
}
