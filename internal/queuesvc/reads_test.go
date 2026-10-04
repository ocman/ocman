package queuesvc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// countingStatus counts every lifecycle read the queue makes.
type countingStatus struct {
	statusStub
	reads atomic.Int32
}

func (c *countingStatus) TurnRunning(ctx context.Context, p, s string) (bool, bool) {
	c.reads.Add(1)
	return c.statusStub.TurnRunning(ctx, p, s)
}

func (c *countingStatus) LatestMessageState(ctx context.Context, p, s string) (string, int64, bool, bool, bool) {
	c.reads.Add(1)
	return c.statusStub.LatestMessageState(ctx, p, s)
}

// Each drain decision reads the session lifecycle once: LatestMessageState
// already reports whether the turn runs, so a TurnRunning read beside it
// only doubles the owner round trip.
func TestDrainDecisionReadsLifecycleOnce(t *testing.T) {
	ctx := context.Background()
	send := func(svc *Service, msg string) {
		t.Helper()
		if err := svc.Enqueue(ctx, "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: msg}); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		running bool
		decide  func(*Service)
		reads   int32
		sent    int
	}{
		{name: "enqueue idle", decide: func(s *Service) { send(s, "a") }, reads: 1, sent: 1},
		{name: "enqueue busy", running: true, decide: func(s *Service) { send(s, "a") }, reads: 1},
		{name: "flush", running: true, decide: func(s *Service) {
			_ = s.Enqueue(ctx, "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "a"})
			s.Flush(ctx, "opencode", "s1")
		}, reads: 1, sent: 1},
		{name: "sweep idle", decide: func(s *Service) {
			_ = s.Enqueue(ctx, "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "a"})
			s.Sweep(ctx)
		}, reads: 1, sent: 1},
		{name: "sweep busy", running: true, decide: func(s *Service) {
			_ = s.Enqueue(ctx, "opencode", true, platforms.SendMessageRequest{SessionID: "s1", Message: "a"})
			s.Sweep(ctx)
		}, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := &countingStatus{statusStub: statusStub{running: tc.running, ok: true}}
			sender := &recSender{}
			tc.decide(New(&memStore{}, sender, status, nil))
			if got := status.reads.Load(); got != tc.reads {
				t.Errorf("lifecycle reads = %d, want %d", got, tc.reads)
			}
			if got := len(sender.messages()); got != tc.sent {
				t.Errorf("sent = %d, want %d", got, tc.sent)
			}
		})
	}
}

// A status reader without LatestMessageState still gates on TurnRunning.
func TestDrainGatesOnTurnRunningWithoutCompletion(t *testing.T) {
	sender := &recSender{}
	svc := New(&memStore{}, sender, onlyTurn{running: true}, nil)
	_ = svc.Enqueue(context.Background(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: "a"})
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent %v into a running turn", got)
	}
}

func TestGuardedSweepReadsLifecycleOnce(t *testing.T) {
	status := &countingStatus{statusStub: statusStub{ok: true, messageID: "user-1", createdAt: 1}}
	sender := &recSender{}
	svc := New(&memStore{}, sender, status, nil)
	for _, message := range []string{"one", "two"} {
		if err := svc.Enqueue(t.Context(), "opencode", false, platforms.SendMessageRequest{SessionID: "s1", Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	status.messageID, status.createdAt, status.completed = "assistant-1", 2, true
	status.reads.Store(0)
	svc.Sweep(t.Context())
	if got := status.reads.Load(); got != 1 {
		t.Fatalf("guarded sweep made %d lifecycle reads, want 1", got)
	}
	if got := sender.messages(); len(got) != 2 || got[1] != "two" {
		t.Fatalf("sent %v, want [one two]", got)
	}
}

type transitionStatus struct{ busy atomic.Bool }

func (s *transitionStatus) TurnRunning(context.Context, string, string) (bool, bool) {
	return s.busy.Load(), true
}

func (s *transitionStatus) LatestMessageState(context.Context, string, string) (string, int64, bool, bool, bool) {
	busy := s.busy.Load()
	return "assistant-2", 2, busy, !busy, true
}

func TestGuardedSweepReadsAfterWaitingForLock(t *testing.T) {
	for _, change := range []string{"busy turn", "replacement guard"} {
		t.Run(change, func(t *testing.T) { testGuardedSweepLockWait(t, change) })
	}
}

func testGuardedSweepLockWait(t *testing.T, change string) {
	t.Helper()
	status := &transitionStatus{}
	sender := &recSender{}
	svc := New(&memStore{}, sender, status, nil)
	key := sessionKey{Platform: "opencode", SessionID: "s1"}
	if err := svc.Enqueue(t.Context(), key.Platform, true, platforms.SendMessageRequest{SessionID: key.SessionID, Message: "held"}); err != nil {
		t.Fatal(err)
	}
	svc.markDrained(key, "user-1", 1)
	unlock := svc.lockFor(key)
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()
	done := make(chan struct{})
	go func() { svc.Sweep(t.Context()); close(done) }()
	// Wait until Sweep is blocked on the per-session lock. A direct Enter
	// send or a native prompt can start a turn without changing the guard.
	deadline := time.After(5 * time.Second)
	for {
		svc.mu.Lock()
		waiting := svc.locks[key].refs == 2
		svc.mu.Unlock()
		if waiting {
			break
		}
		select {
		case <-deadline:
			t.Fatal("sweep did not wait for the session lock")
		case <-time.After(time.Millisecond):
		}
	}
	if change == "busy turn" {
		status.busy.Store(true)
	} else {
		svc.markDrained(key, "replacement", 3)
	}
	unlock()
	unlock = nil
	select {
	case <-done:
	case <-deadline:
		t.Fatal("sweep did not finish")
	}
	if got := sender.messages(); len(got) != 0 {
		t.Fatalf("sent %v after %s while sweep waited for the lock", got, change)
	}
}

type onlyTurn struct{ running bool }

func (o onlyTurn) TurnRunning(context.Context, string, string) (bool, bool) { return o.running, true }
