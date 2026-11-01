package conversationingest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

type disappearingFeed struct{ *fakeFeed }

func (f *disappearingFeed) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	if id == "a-helper" {
		return nil, fmt.Errorf("source snapshot: %w", platforms.ErrNotFound)
	}
	return f.fakeFeed.HistorySnapshot(ctx, id)
}

func disappearanceWorker(t *testing.T) (*sourceWorker, *fakeFeed) {
	t.Helper()
	f := newFakeFeed()
	w, _ := feedImporter(t, f)
	w.drainFeed(t.Context())
	source := &disappearingFeed{f}
	w.source.Reader, w.feed = source, source
	return w, f
}

func TestDeletedHelperDoesNotWarnOrDelayHealthySession(t *testing.T) {
	w, f := disappearanceWorker(t)
	var warnings []error
	w.module.deps.OnError = func(_, _ string, err error) { warnings = append(warnings, err) }
	f.snapshots["session"] = historySnapshot("healthy new reply")
	f.sequences["session"] = 4
	w.enqueue(platforms.HistoryChange{SessionID: "a-helper", Seq: 2})
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, Resync: true})
	w.drainFeed(t.Context())
	if len(warnings) != 0 {
		t.Errorf("normal helper cleanup caused repair warnings: %v", warnings)
	}
	if text, _ := importText(t, w.module.deps.Store, feedKey(w)); text != "healthy new reply" {
		t.Fatalf("deleted helper delayed healthy session: %q", text)
	}
}

func TestFeedFailurePreservesDeletionAndProcessesHealthyEvents(t *testing.T) {
	w, f := disappearanceWorker(t)
	f.snapshots["session"] = historySnapshot("healthy new reply")
	f.sequences["session"] = 4
	// The helper is still listed: not-found alone cannot authorize deletion.
	f.entries = append(f.entries, db.HistoryEntry{ID: "a-helper", Version: "v1"})
	w.enqueue(platforms.HistoryChange{SessionID: "a-helper", Seq: 2})
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, Resync: true})
	w.enqueue(platforms.HistoryChange{SessionID: "z-deleted", Seq: 3, Deleted: true})
	w.drainFeed(t.Context())
	if text, _ := importText(t, w.module.deps.Store, feedKey(w)); text != "healthy new reply" {
		t.Errorf("one failed session blocked healthy content: %q", text)
	}
	checkpoint, err := w.module.deps.Store.Checkpoint(t.Context(), w.instance.ID, sequencePrefix+"z-deleted")
	if err != nil || checkpoint != "seq:3" {
		t.Errorf("one failed session blocked deletion: %q %v", checkpoint, err)
	}
	// Source-wide failures must preserve the original event, not just its ID.
	w.enqueue(platforms.HistoryChange{SessionID: "z-deleted", Seq: 4, Deleted: true})
	f.identityErr = errors.New("source offline")
	w.drainFeed(t.Context())
	w.feedState.mu.Lock()
	defer w.feedState.mu.Unlock()
	change := w.feedState.pending["z-deleted"]
	if change == nil || !change.Deleted || change.Seq != 4 {
		t.Fatalf("retry lost deletion semantics: %+v", change)
	}
}

func TestMissingSnapshotRequiresCompleteInventory(t *testing.T) {
	for _, inventoryErr := range []error{errors.New("offline"), &platforms.HistoryDeferred{Count: 1}} {
		t.Run(inventoryErr.Error(), func(t *testing.T) {
			w, f := disappearanceWorker(t)
			f.inventoryErr = inventoryErr
			w.enqueue(platforms.HistoryChange{SessionID: "a-helper", Seq: 2})
			w.drainFeed(t.Context())
			if w.lastFailure == "" {
				t.Fatal("failed or incomplete inventory authorized disappearance")
			}
		})
	}
}

func TestAbsentSessionRefreshReturnsNotFoundAndClearsPriority(t *testing.T) {
	for _, catchUp := range []bool{false, true} {
		t.Run(fmt.Sprint(catchUp), func(t *testing.T) {
			w, _ := disappearanceWorker(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- w.module.RefreshStored(ctx, "feed", "a-helper") }()
			select {
			case <-w.wake:
			case <-ctx.Done():
				t.Fatal("refresh did not request read")
			}
			w.feedState.catchUp = catchUp
			w.drainFeed(t.Context())
			if err := <-done; !errors.Is(err, db.ErrSessionNotFound) {
				t.Fatalf("absent session refresh = %v, want not-found without timeout", err)
			}
			if w.priority["a-helper"] || w.dirty["a-helper"] {
				t.Fatalf("absent refresh still queued: priority=%v dirty=%v", w.priority, w.dirty)
			}
			if w.explicitReadPending("a-helper") {
				t.Fatal("terminal not-found result left a refresh pending")
			}
		})
	}
}

func TestConfirmedDisappearanceRetainsConversation(t *testing.T) {
	w, f := disappearanceWorker(t)
	f.entries = nil
	f.snapshotErr = platforms.ErrNotFound
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	s, err := w.module.deps.Store.SessionState(t.Context(), feedKey(w))
	if err != nil || !s.SourceDeleted {
		t.Fatalf("confirmed disappearance was not retained as deleted: %+v %v", s, err)
	}
	if text, _ := importText(t, w.module.deps.Store, feedKey(w)); text != "feed" {
		t.Fatal("disappearance discarded retained content")
	}
	if _, err := w.module.deps.Store.ReadConversation(t.Context(), statev2.Key{InstanceID: w.instance.ID, SessionID: "a-helper"}, 1, 0); !errors.Is(err, statev2.ErrNotFound) {
		t.Fatalf("invented helper conversation: %v", err)
	}
}

type inventoryObservedFeed struct {
	*disappearingFeed
	afterInventory func()
}

func (f *inventoryObservedFeed) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	entries, err := f.fakeFeed.HistoryInventory(ctx)
	if f.afterInventory != nil {
		f.afterInventory()
	}
	return entries, err
}

func TestAbsenceCannotResolveNewerRequestOrDiscardNewInvalidation(t *testing.T) {
	w, f := disappearanceWorker(t)
	old := w.module.nextReadTicket("feed", "a-helper")
	w.module.requestRead("feed", "a-helper")
	var newer uint64
	source := &inventoryObservedFeed{disappearingFeed: &disappearingFeed{f}}
	source.afterInventory = func() {
		newer = w.module.nextReadTicket("feed", "a-helper")
		w.module.requestRead("feed", "a-helper")
	}
	w.source.Reader, w.feed = source, source
	w.feedState.catchUp = true
	w.drainFeed(t.Context())
	key := [2]string{"feed", "a-helper"}
	if w.module.readMissing[key] != old || w.module.readMissing[key] >= newer {
		t.Fatalf("old inventory resolved newer ticket: missing=%d old=%d newer=%d", w.module.readMissing[key], old, newer)
	}
	if !w.priority["a-helper"] || !w.dirty["a-helper"] {
		t.Fatal("old inventory discarded a newer requested read")
	}
	source.afterInventory = nil
	w.drainFeed(t.Context())
	if w.module.readMissing[key] != newer || w.module.readValidated[key] != 0 {
		t.Fatal("new absence validation did not resolve only as missing")
	}
}

func TestPollingAbsentRefreshAndInitialReadFinishWithoutTimeout(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			f := fakeSource("one")
			f.entries = nil
			m, _ := testImporter(t, f)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if initial {
					if m.AwaitStored(ctx, "one", "missing") {
						done <- errors.New("missing session was readable")
					} else {
						done <- ctx.Err()
					}
				} else {
					done <- m.RefreshStored(ctx, "one", "missing")
				}
			}()
			select {
			case <-m.workers[0].wake:
			case <-ctx.Done():
				t.Fatal("no read requested")
			}
			m.workers[0].sweep(ctx)
			err := <-done
			if initial && err != nil || !initial && !errors.Is(err, db.ErrSessionNotFound) {
				t.Fatalf("absence result = %v", err)
			}
		})
	}
}

func TestDisappearanceDoesNotDiscardNewActivityHint(t *testing.T) {
	w, f := disappearanceWorker(t)
	source := &inventoryObservedFeed{disappearingFeed: &disappearingFeed{f}, afterInventory: func() {
		w.module.Observe("feed", "a-helper")
	}}
	w.source.Reader, w.feed = source, source
	w.enqueue(platforms.HistoryChange{SessionID: "a-helper", Seq: 2})
	w.drainFeed(t.Context())
	if _, ok := w.dirty["a-helper"]; !ok {
		t.Fatal("absence reconciliation discarded an activity hint arriving during inventory")
	}
}

func TestLiveMessageReadReconcilesDisappearanceWithoutLosingHistory(t *testing.T) {
	for _, status := range []string{"gone", "still listed", "inventory offline"} {
		t.Run(status, func(t *testing.T) {
			f := newFakeFeed()
			w, store := feedImporter(t, f)
			w.drainFeed(t.Context())
			f.messageErr = platforms.ErrNotFound
			if status != "still listed" {
				f.entries = nil
			}
			if status == "inventory offline" {
				f.inventoryErr = errors.New("inventory offline")
			}
			w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}})
			w.drainFeed(t.Context())
			s, err := store.SessionState(t.Context(), feedKey(w))
			if err != nil || s.SourceDeleted != (status == "gone") {
				t.Fatalf("disappearance state = %+v %v", s, err)
			}
			if (w.lastFailure == "") != (status == "gone") {
				t.Fatalf("unexpected failure status: %q", w.lastFailure)
			}
			if text, _ := importText(t, store, feedKey(w)); text != "feed" {
				t.Fatal("missing live message read discarded history")
			}
		})
	}
}

func TestInvalidInventoryCannotConfirmDisappearance(t *testing.T) {
	for _, entries := range [][]db.HistoryEntry{{{ID: ""}}, {{ID: "duplicate"}, {ID: "duplicate"}}} {
		w, f := disappearanceWorker(t)
		f.entries = entries
		if gone, err := w.reconcileMissing(t.Context(), "a-helper", 1); gone || err == nil {
			t.Fatalf("invalid inventory confirmed disappearance: %v %v", gone, err)
		}
	}
}

func TestAbsenceStorageFailureDoesNotResolveRefresh(t *testing.T) {
	w, _ := disappearanceWorker(t)
	ticket := w.module.nextReadTicket("feed", "session")
	stored, err := w.module.deps.Store.SessionState(t.Context(), feedKey(w))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.module.deps.Store.Close(); err != nil {
		t.Fatal(err)
	}
	if err := w.resolveMissingState(t.Context(), "session", ticket, stored); err == nil {
		t.Fatal("failed storage was acknowledged as absence")
	}
	if w.module.readMissing[[2]string{"feed", "session"}] != 0 {
		t.Fatal("storage failure resolved refresh as missing")
	}
}

func TestPollingDeferredInventoryKeepsMissingRefreshPending(t *testing.T) {
	f := fakeSource("one")
	f.entries = nil
	f.inventoryErr = &platforms.HistoryDeferred{Count: 1}
	m, _ := testImporter(t, f)
	ticket := m.nextReadTicket("one", "missing")
	m.requestRead("one", "missing")
	m.workers[0].sweep(t.Context())
	key := [2]string{"one", "missing"}
	if m.readMissing[key] != 0 || !m.workers[0].priority["missing"] {
		t.Fatal("incomplete inventory resolved absent refresh")
	}
	f.inventoryErr = nil
	m.workers[0].sweep(t.Context())
	if m.readMissing[key] != ticket || m.workers[0].priority["missing"] {
		t.Fatal("complete inventory did not resolve the deferred refresh")
	}
}

func TestAbsenceCheckCannotDeleteNewerCommittedContent(t *testing.T) {
	for _, catchUp := range []bool{false, true} {
		t.Run(fmt.Sprint(catchUp), func(t *testing.T) {
			w, f := disappearanceWorker(t)
			f.entries = nil
			ticket := w.module.nextReadTicket("feed", "session")
			source := &inventoryObservedFeed{disappearingFeed: &disappearingFeed{f}, afterInventory: func() {
				batch, _, err := Normalize(feedKey(w), historySnapshot("newer content"))
				if err != nil {
					t.Fatal(err)
				}
				batch.ExpectedRevision = 1
				if _, err := w.module.deps.Store.Commit(t.Context(), batch); err != nil {
					t.Fatal(err)
				}
			}}
			w.source.Reader, w.feed = source, source
			var err error
			if catchUp {
				err = w.catchUpFeed(t.Context(), map[string]bool{"session": true}, nil)
			} else {
				_, err = w.reconcileMissing(t.Context(), "session", ticket)
			}
			if !errors.Is(err, statev2.ErrConflict) {
				t.Fatalf("stale absence check = %v, want revision conflict", err)
			}
			stored, err := w.module.deps.Store.SessionState(t.Context(), feedKey(w))
			if err != nil || stored.SourceDeleted {
				t.Fatalf("old inventory deleted newer content: %+v %v", stored, err)
			}
			if w.module.readMissing[[2]string{"feed", "session"}] != 0 {
				t.Fatal("conflicting absence check resolved the refresh")
			}
		})
	}
}
