package convstream

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

type blockedSettler struct {
	*fakeSource
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockedSettler) Settle(ctx context.Context) (*platforms.SessionLifecycle, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &platforms.SessionLifecycle{Status: db.StatusError}, nil
	}
	return &platforms.SessionLifecycle{Status: db.StatusDone}, nil
}

func TestSettlementBurstCoalescesAndDiscardsStaleOutcome(t *testing.T) {
	src := &blockedSettler{fakeSource: newFakeSource(), started: make(chan struct{}), release: make(chan struct{})}
	src.seed = Seed{Status: &convevents.SessionStatusEvent{Status: db.StatusBusy}}
	var release sync.Once
	defer release.Do(func() { close(src.release) })
	h := newTestHub(t, Options{})
	sub := subscribe(t, h, src, "")
	sink := nextSink(t, src.fakeSource)
	sink.Emit(idle(), Meta{})
	<-src.started
	for range 99 {
		sink.Emit(status(db.StatusBusy), Meta{})
		sink.Emit(idle(), Meta{})
		drain(sub)
	}
	release.Do(func() { close(src.release) })
	got := await(t, sub, convevents.TypeSessionStatus)
	for _, ev := range got {
		if st, ok := ev.Event.(convevents.SessionStatusEvent); ok && st.Status == db.StatusError {
			t.Fatal("obsolete settlement overwrote the current turn")
		}
	}
	h.Close()
	h.opts.Workers.Close()
	if got := src.calls.Load(); got != 2 {
		t.Fatalf("100 idle transitions performed %d reads, want one in-flight and one trailing read", got)
	}
}

func TestSettlementDiscardsDetachedOrReconnectedRead(t *testing.T) {
	for _, detach := range []bool{false, true} {
		t.Run(map[bool]string{false: "reconnected", true: "detached"}[detach], func(t *testing.T) {
			src := &blockedSettler{fakeSource: newFakeSource(), started: make(chan struct{}), release: make(chan struct{})}
			h := newTestHub(t, Options{})
			sub := subscribe(t, h, src, "")
			sink := nextSink(t, src.fakeSource)
			sink.Emit(idle(), Meta{})
			<-src.started
			sink.Emit(status(db.StatusBusy), Meta{})
			sink.Emit(idle(), Meta{})
			drain(sub)
			sub.s.mu.Lock()
			if detach {
				sub.s.detached = true
			} else {
				sub.s.gen++
			}
			sub.s.mu.Unlock()
			close(src.release)
			deadline := time.Now().Add(waitFor)
			for {
				sub.s.mu.Lock()
				reading := sub.s.settlement.reading
				sub.s.mu.Unlock()
				if !reading {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("settlement did not finish")
				}
				time.Sleep(time.Millisecond)
			}
			if src.calls.Load() != 1 || len(drain(sub)) != 0 {
				t.Fatal("obsolete source read published or admitted trailing work")
			}
			h.Close()
			h.opts.Workers.Close()
		})
	}
}

func TestSettlementRefusesStoppedWorkers(t *testing.T) {
	h := newTestHub(t, Options{})
	h.opts.Workers.Close()
	s := newSession(h, testKey)
	s.source = newFakeSource()
	s.scheduleSettleLocked(s.st.gen, false)
	if s.settlement.reading || s.settlement.pending != nil {
		t.Fatal("refused worker left settlement stuck")
	}
	s.detached = true
	s.scheduleSettleLocked(s.st.gen, false)
}
