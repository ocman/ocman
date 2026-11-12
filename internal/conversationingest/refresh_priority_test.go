package conversationingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// Use actual concurrent RefreshStored calls, as the new-row and title watcher
// callbacks do, while a live batch is already draining on the source worker.
func TestConcurrentRefreshesBypassUnrelatedLiveFeedBatch(t *testing.T) {
	base := newFakeFeed()
	base.fakeHistory = bulkHistory(3)
	base.sequences = map[string]int64{"session-0": 3, "session-1": 3, "session-2": 3}
	f := &priorityFeed{fakeFeed: base}
	m, _ := testImporter(t, base.fakeHistory)
	w := m.workers[0]
	w.source.Reader, w.feed = f, f
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	started, release, blocked := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(blocked) })
	f.onRead = func(id string) {
		switch id {
		case "session-0":
			close(started)
			<-release
		case "session-1":
			<-blocked
		}
	}
	drained := make(chan error, 1)
	go func() {
		drained <- w.applyChanges(t.Context(), map[string]*platforms.HistoryChange{
			"session-0": {SessionID: "session-0", Seq: 3, Resync: true},
			"session-1": {SessionID: "session-1", Seq: 3, Resync: true},
		}, map[string]bool{})
	}()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
	defer cancel()
	results := make(chan error, 2)
	for range 2 {
		go func() { results <- m.RefreshStored(ctx, "bulk", "session-2") }()
	}
	eventually(t, "both callers did not request validation", func() bool { return m.readTicket("bulk", "session-2") == 2 })
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("refresh starved behind unrelated live batch: %v", err)
		}
	}
	unblock.Do(func() { close(blocked) })
	if err := <-drained; err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, id := range f.order {
		if id == "session-2" {
			reads++
		}
	}
	if reads != 1 {
		t.Errorf("two queued callers need one post-request snapshot, got %d", reads)
	}
}

func TestPriorityRefreshDoesNotAcknowledgeCallerArrivingDuringSnapshot(t *testing.T) {
	f := newFakeFeed()
	w, _ := feedImporter(t, f)
	w.drainFeed(t.Context())
	m := w.module
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- m.RefreshStored(ctx, "feed", "session") }()
	eventually(t, "first caller did not request validation", func() bool { return m.readTicket("feed", "session") == 1 })
	started, release := make(chan struct{}), make(chan struct{})
	f.mu.Lock()
	f.afterSnapshot = func() { close(started); <-release }
	f.mu.Unlock()
	served := make(chan struct{})
	go func() {
		defer close(served)
		w.serveFeedRequests(ctx, nil, nil)
	}()
	<-started
	go func() { second <- m.RefreshStored(ctx, "feed", "session") }()
	eventually(t, "second caller did not request validation", func() bool { return m.readTicket("feed", "session") == 2 })
	close(release)
	<-served
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		t.Fatalf("older priority snapshot acknowledged newer caller: %v", err)
	default:
	}
	f.mu.Lock()
	f.afterSnapshot = nil
	f.mu.Unlock()
	w.drainFeed(ctx)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestRefreshReceiptCannotCrossSourceRoutingIdentity(t *testing.T) {
	m, _ := testImporter(t, fakeSource("first"), fakeSource("second"))
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- m.RefreshStored(ctx, "first", "session") }()
	go func() { second <- m.RefreshStored(ctx, "second", "session") }()
	eventually(t, "callers did not request both sources", func() bool {
		return m.readTicket("first", "session") == 1 && m.readTicket("second", "session") == 1
	})
	m.workers[0].sweep(ctx)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-second:
		t.Fatalf("other source acknowledged this source's refresh: %v", err)
	default:
	}
	m.workers[1].sweep(ctx)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}
