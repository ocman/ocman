package conversationingest

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/tasksvc"
)

func TestFeedRetainsFailedAndDeferredExplicitRepairs(t *testing.T) {
	for _, deferred := range []bool{false, true} {
		t.Run(fmt.Sprint(deferred), func(t *testing.T) {
			f := newFakeFeed()
			w, store := feedImporter(t, f)
			w.drainFeed(t.Context())
			f.snapshots["session"] = historySnapshot("repaired")
			if deferred {
				f.changingReads = 3
			} else {
				f.snapshotErr = errors.New("offline")
			}
			w.module.Notify("feed", "session")
			w.drainFeed(t.Context())
			if _, ready := w.module.ReadyInstances(); ready {
				t.Fatal("failed repair reported ready")
			}
			f.snapshotErr = nil
			w.drainFeed(t.Context())
			if text, _ := importText(t, store, feedKey(w)); text != "repaired" {
				t.Fatalf("unchanged sequence lost repair: %q", text)
			}
			if got := receipt(t, store, w); got != db.SequenceVersion(3, true) {
				t.Fatalf("receipt = %s", got)
			}
		})
	}
}

func TestFeedCatchUpRecordsIdenticalFailureOnce(t *testing.T) {
	f := newFakeFeed()
	f.snapshotErr = errors.New("offline")
	w, _ := feedImporter(t, f)
	manager := taskManager(t, w.module)
	for range 4 {
		w.drainFeed(t.Context())
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("identical feed failure created %d tasks", len(tasks))
	}
}

func TestFeedEnqueueBoundsAndDeduplicates(t *testing.T) {
	w, _ := feedImporter(t, newFakeFeed())
	for range 10000 {
		w.enqueue(platforms.HistoryChange{SessionID: "same", Seq: 3, MessageIDs: []string{"m"}})
	}
	if got := len(w.feedState.pending["same"].MessageIDs); got != 1 {
		t.Fatalf("duplicate hints retained %d IDs", got)
	}
	for i := range 10000 {
		w.enqueue(platforms.HistoryChange{SessionID: fmt.Sprint(i), Seq: 3, MessageIDs: []string{"m"}})
	}
	if len(w.feedState.pending) > 4096 || !w.feedState.catchUp {
		t.Fatalf("unbounded queue: %d, catch-up %v", len(w.feedState.pending), w.feedState.catchUp)
	}
}
