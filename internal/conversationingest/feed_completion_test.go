package conversationingest

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestFeedCompletionRepairIncludesReplyNotNamedByPendingEvent(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "idle invalidation"
		if explicit {
			name = "refresh ticket"
		}
		t.Run(name, func(t *testing.T) {
			f := newFakeFeed()
			w, store := feedImporter(t, f)
			w.drainFeed(t.Context())
			// The source has completed the reply, but the retained conversation
			// missed it. The pending idle event does not name that reply.
			f.snapshots["session"].Messages = append(f.snapshots["session"].Messages,
				db.Message{ID: "reply", SessionID: "session", TimeCreated: 5,
					Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)})
			f.snapshots["session"].Parts = append(f.snapshots["session"].Parts,
				db.Part{ID: "reply-text", SessionID: "session", MessageID: "reply",
					Data: json.RawMessage(`{"type":"text","text":"microphone links"}`)})
			f.sequences["session"] = 4
			w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4})
			var refreshed chan error
			if explicit {
				<-w.wake // Consume the pending event's signal before requesting refresh.
				ctx, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				refreshed = make(chan error, 1)
				go func() { refreshed <- w.module.RefreshStored(ctx, "feed", "session") }()
				select {
				case <-w.wake:
				case <-ctx.Done():
					t.Fatal("refresh did not request a read")
				}
			} else {
				w.module.Notify("feed", "session")
			}
			w.drainFeed(t.Context())
			page, err := store.ReadConversation(t.Context(), feedKey(w), 30, 0)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, p := range page.Parts {
				found = found || p.Text == "microphone links"
			}
			if !found {
				t.Fatal("completed reply still missing after repair; another prompt would be needed to reveal it")
			}
			if explicit {
				if err := <-refreshed; err != nil {
					t.Fatalf("refresh was not acknowledged after the complete read: %v", err)
				}
			}
		})
	}
}

func TestFeedPendingEventCannotSkipSameSequenceRepair(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshots["session"] = historySnapshot("completed response")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 3})
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "completed response" {
		t.Fatalf("same-sequence event swallowed repair: %q", text)
	}
}

func TestFeedPendingRepairFailureDoesNotAcknowledgeRefresh(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshots["session"] = historySnapshot("completed response")
	f.snapshotErr = errors.New("source unavailable")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 3})
	w.module.readRequests = map[[2]string]uint64{{"feed", "session"}: 1}
	w.module.requestRead("feed", "session")
	w.drainFeed(t.Context())
	if w.module.readValidated[[2]string{"feed", "session"}] != 0 {
		t.Fatal("failed full repair acknowledged the refresh ticket")
	}
	if !w.dirty["session"] {
		t.Fatal("failed full repair was not retained for retry")
	}
	f.snapshotErr = nil
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "completed response" {
		t.Fatalf("retry left completed response missing: %q", text)
	}
	if w.module.readValidated[[2]string{"feed", "session"}] != 1 {
		t.Fatal("successful retry did not acknowledge the refresh ticket")
	}
}

func TestFeedPendingDeletionDoesNotReadMissingSourceForRepair(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshotErr = errors.New("session was deleted")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, Deleted: true})
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	s, err := store.SessionState(t.Context(), feedKey(w))
	if err != nil || !s.SourceDeleted {
		t.Fatalf("pending deletion did not retain the source-deleted session: %+v %v", s, err)
	}
	if f.reads != 1 {
		t.Fatalf("deletion repair attempted to read the deleted source: %d reads", f.reads)
	}
}
