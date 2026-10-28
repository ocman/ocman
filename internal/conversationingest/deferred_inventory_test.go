package conversationingest

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func TestDeferredInventoryImportsReadableSessionsWithoutDeletingOrClaimingReadiness(t *testing.T) {
	for _, feed := range []bool{false, true} {
		t.Run(map[bool]string{false: "polling", true: "feed"}[feed], func(t *testing.T) {
			f := newFakeFeed()
			w, store := feedImporter(t, f)
			w.drainFeed(t.Context())
			key := feedKey(w)
			// The retained original disappears from a partial inventory. It must
			// not be marked deleted, while another resolvable row can still import.
			fresh := historySnapshot("fresh")
			fresh.Session.ID = "fresh"
			for i := range fresh.Messages {
				fresh.Messages[i].ID = "fresh-" + fresh.Messages[i].ID
				fresh.Messages[i].SessionID = "fresh"
			}
			for i := range fresh.Parts {
				fresh.Parts[i].ID = "fresh-" + fresh.Parts[i].ID
				fresh.Parts[i].MessageID = "fresh-" + fresh.Parts[i].MessageID
				fresh.Parts[i].SessionID = "fresh"
			}
			f.entries = []db.HistoryEntry{{ID: "fresh", Version: "v1"}}
			f.snapshots["fresh"] = fresh
			f.sequences["fresh"] = 1
			f.inventoryErr = &platforms.HistoryDeferred{Count: 1, Err: errors.New("attached supervisor unavailable")}
			if feed {
				w.feedState.catchUp = true
				w.drainFeed(t.Context())
			} else {
				w.feed = nil
				if err := w.sweepChecked(t.Context()); err == nil {
					t.Fatal("incomplete inventory reported a successful pass")
				}
			}
			if _, ready := w.module.ReadyInstances(); ready {
				t.Fatal("partial inventory enabled complete-history analytics")
			}
			old, err := store.SessionState(t.Context(), key)
			if err != nil || old.SourceDeleted {
				t.Fatalf("partial inventory deleted retained session: %+v %v", old, err)
			}
			key.SessionID = "fresh"
			got, err := store.SessionState(t.Context(), key)
			if err != nil || got.Revision == 0 || w.last.Counters["deferred"] != 1 {
				t.Fatalf("readable session not imported: %+v %v progress=%+v", got, err, w.last)
			}
		})
	}
}
