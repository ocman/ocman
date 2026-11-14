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
	var releaseFirst, unblock sync.Once
	f.onRead = func(id string) {
		switch id {
		case "session-0":
			close(started)
			<-release
		case "session-1":
			<-blocked
		}
	}
	drained := make(chan struct{})
	var drainErr error
	go func() {
		defer close(drained)
		drainErr = w.applyChanges(t.Context(), map[string]*platforms.HistoryChange{
			"session-0": {SessionID: "session-0", Seq: 3, Resync: true},
			"session-1": {SessionID: "session-1", Seq: 3, Resync: true},
		}, map[string]bool{})
	}()
	ctx, cancel := context.WithCancel(t.Context())
	var callers sync.WaitGroup
	defer func() {
		cancel()
		releaseFirst.Do(func() { close(release) })
		unblock.Do(func() { close(blocked) })
		<-drained
		callers.Wait()
	}()
	<-started
	queued := make(chan struct{}, 2)
	results := make(chan error, 2)
	for range 2 {
		callers.Go(func() {
			results <- m.RefreshStored(&queuedRefreshContext{Context: ctx, queued: queued}, "bulk", "session-2")
		})
	}
	// Ticket allocation precedes requestRead. Reaching RefreshStored's wait
	// select proves each caller has actually published its priority request.
	eventually(t, "both callers did not queue validation", func() bool { return len(queued) == 2 })
	releaseFirst.Do(func() { close(release) })
	watchdog := time.NewTimer(5 * time.Second)
	defer watchdog.Stop()
	for range 2 {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("refresh failed: %v", err)
			}
		case <-watchdog.C:
			t.Fatal("refresh starved behind unrelated live batch")
		}
	}
	unblock.Do(func() { close(blocked) })
	<-drained
	if drainErr != nil {
		t.Fatal(drainErr)
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

// RefreshStored first consults Done in its receipt wait, after requestRead.
// Observe that boundary without adding production hooks or polling tickets.
type queuedRefreshContext struct {
	context.Context
	queued chan<- struct{}
	once   sync.Once
}

func (c *queuedRefreshContext) Done() <-chan struct{} {
	c.once.Do(func() { c.queued <- struct{}{} })
	return c.Context.Done()
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
