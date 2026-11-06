package conversationingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

type failingWatch struct {
	*fakeFeed
	attempts chan struct{}
}

func (f *failingWatch) WatchHistory(context.Context, func(), func(platforms.HistoryChange)) error {
	f.attempts <- struct{}{}
	return errors.New("offline")
}

func TestRequestedReadWakesReconnectButOrdinaryEventsDoNot(t *testing.T) {
	f := &failingWatch{fakeFeed: newFakeFeed(), attempts: make(chan struct{}, 10)}
	m := NewModule(Deps{Sources: []Source{{AdapterID: "feed", Reader: f}}})
	w := m.workers[0]
	w.feed = f
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); w.watchFeed(ctx) }()
	defer func() { cancel(); <-done }()
	<-f.attempts
	for range 100 {
		m.Observe("feed", "session")
	}
	select {
	case <-f.attempts:
		t.Fatal("ordinary events bypassed reconnect delay")
	case <-time.After(50 * time.Millisecond):
	}
	m.requestRead("feed", "session")
	select {
	case <-f.attempts:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("requested read waited behind reconnect backoff")
	}
}

func TestReconnectBackoffCapsJittersAndResetsAfterHealthyWatch(t *testing.T) {
	var retry reconnectBackoff
	var previous time.Duration
	var varied bool
	for i := range 100 {
		delay := retry.after(0)
		base := min(2*time.Second*time.Duration(1<<min(i, 4)), 30*time.Second)
		if delay < base*3/4 || delay > min(base*5/4, 30*time.Second) {
			t.Fatalf("attempt %d: delay %s", i, delay)
		}
		if i > 5 && delay != previous {
			varied = true
		}
		previous = delay
	}
	if !varied {
		t.Fatal("reconnect attempts have no jitter")
	}
	if delay := retry.after(time.Minute); delay < 1500*time.Millisecond || delay > 2500*time.Millisecond {
		t.Fatalf("healthy watch did not reset: %s", delay)
	}
}

func TestReconnectWaitCancellationAndTimer(t *testing.T) {
	if !waitReconnect(t.Context(), nil, time.Nanosecond) {
		t.Fatal("timer did not retry")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if waitReconnect(ctx, nil, time.Hour) {
		t.Fatal("cancellation retried")
	}
}

func TestRequestedFeedReadBypassesBackgroundCooldown(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshots["session"] = historySnapshot("requested repair")
	w.module.requestRead("feed", "session")
	w.serveFeedPriority(t.Context(), retryBackoff{next: time.Now().Add(time.Hour)})
	if text, _ := importText(t, store, feedKey(w)); text != "requested repair" {
		t.Fatalf("read waited for background retry: %q", text)
	}
	reads := f.reads
	w.module.requestRead("feed", "session")
	w.serveFeedPriority(t.Context(), retryBackoff{})
	if f.reads != reads {
		t.Fatal("healthy loop performed an extra priority read before its normal drain")
	}
}
