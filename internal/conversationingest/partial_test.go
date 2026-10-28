package conversationingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
)

func TestPartialRuntimeSnapshotCannotEraseRetainedMessagesOrAccounting(t *testing.T) {
	source := fakeSource("first")
	source.snapshots["session"] = accountedSnapshot()
	m, store := testImporter(t, source)
	w := m.workers[0]
	repair(t, w, nil)
	before, err := store.Usage(t.Context(), sourceKey("first"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := *source.snapshots["session"]
	snapshot.Partial = true
	snapshot.Messages = nil
	snapshot.Parts = nil
	snapshot.Version = "runtime:session"
	source.snapshots["session"] = &snapshot
	source.entries = []db.HistoryEntry{{ID: "session", Version: snapshot.Version}}
	repair(t, w, map[string]bool{"session": true})
	after, err := store.Usage(t.Context(), sourceKey("first"))
	if err != nil || before != after {
		t.Fatalf("partial bootstrap erased accounting: before=%+v after=%+v %v", before, after, err)
	}
	page, err := store.ReadConversation(t.Context(), sourceKey("first"), 100, 0)
	if err != nil || len(page.Messages) == 0 {
		t.Fatalf("partial bootstrap erased content: %+v %v", page, err)
	}
	if w.last.Counters["usage-mismatch"] != 0 || len(w.failing) != 0 {
		t.Fatalf("partial input was treated as complete accounting: %+v %v", w.last, w.failing)
	}
	if !m.AwaitStored(t.Context(), "first", "session") {
		t.Fatal("validated stored snapshot not available")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if m.AwaitStored(ctx, "missing", "session") {
		t.Fatal("canceled wait succeeded")
	}
	var absent *Module
	if absent.AwaitStored(t.Context(), "first", "session") {
		t.Fatal("nil ingestion accepted")
	}
}

func TestRefreshWaitsForItsOwnSessionAndForAReadStartedAfterTheRequest(t *testing.T) {
	source := fakeSource("first")
	m, _ := testImporter(t, source)
	w := m.workers[0]
	repair(t, w, nil)
	started, release := make(chan struct{}), make(chan struct{})
	source.afterSnapshot = func() { close(started); <-release }
	oldDone := make(chan error, 1)
	go func() { oldDone <- w.repair(t.Context(), map[string]bool{"session": true}) }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	refreshed := make(chan error, 1)
	go func() { refreshed <- m.RefreshStored(ctx, "first", "session") }()
	for deadline := time.Now().Add(time.Second); m.readTicket("first", "session") == 0; {
		if time.Now().After(deadline) {
			t.Fatal("refresh did not request a ticket")
		}
		time.Sleep(time.Millisecond)
	}
	m.publishReceipt("first", "unrelated")
	close(release)
	if err := <-oldDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-refreshed:
		t.Fatalf("pre-request snapshot satisfied refresh: %v", err)
	default:
	}
	source.afterSnapshot = nil
	repair(t, w, map[string]bool{"session": true})
	if err := <-refreshed; err != nil {
		t.Fatal(err)
	}
	canceled, stop := context.WithCancel(t.Context())
	stop()
	if err := m.RefreshStored(canceled, "first", "session"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := m.RefreshStored(t.Context(), "first", ""); err == nil {
		t.Fatal("empty refresh accepted")
	}
}
