package conversationingest

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/platforms"
)

func TestJournalKnownSessionDoesNotInventorySource(t *testing.T) {
	m, src, store := journalModule(t, 0)
	w := m.workers[0]
	w.journaled = true
	w.sweep(t.Context())
	// Once bootstrapped, a session-specific hook must not scan every transcript.
	src.inventoryErr = errors.New("source-wide inventory must not run")
	src.snapshots["session"] = historySnapshot("updated")
	if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session"}}); err != nil {
		t.Fatal(err)
	}
	if text, _ := importText(t, store, sourceKey("claude")); text != "updated" {
		t.Fatalf("hook update lost: %q", text)
	}
}

type deltaHistory struct {
	*fakeHistory
	requested []string
	snapshot  *db.HistorySnapshot
}

func (f *deltaHistory) HistoryDelta(_ context.Context, _ string, cursor string) (*db.HistorySnapshot, error) {
	f.requested = append(f.requested, cursor)
	return f.snapshot, nil
}

func TestTranscriptCursorCommitsWithContentAndResumesAfterRestart(t *testing.T) {
	f := &deltaHistory{fakeHistory: fakeSource("delta"), snapshot: historySnapshot("retained")}
	f.snapshot.SourceCursor = "offset-1"
	m, store := testImporter(t, f.fakeHistory)
	w := m.workers[0]
	w.source.Reader, w.journaled = f, true
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importSession(t.Context(), f.entries[0]); err != nil {
		t.Fatal(err)
	}
	// Restart with no in-memory offsets; the persisted snapshot checkpoint is
	// the only input to the next read. This delta must preserve older history.
	restarted := NewModule(m.deps).workers[0]
	restarted.source.Reader, restarted.journaled = f, true
	if err := restarted.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.snapshot = historySnapshot("new message")
	f.snapshot.Partial, f.snapshot.MessageOffset, f.snapshot.SourceCursor = true, 1, "offset-2"
	f.snapshot.Messages[0].ID = "new-message"
	f.snapshot.Parts[0].ID, f.snapshot.Parts[0].MessageID = "new-part", "new-message"
	if _, err := restarted.importSession(t.Context(), f.entries[0]); err != nil {
		t.Fatal(err)
	}
	if len(f.requested) != 2 || f.requested[0] != "" || f.requested[1] != "offset-1" {
		t.Fatalf("source did not resume from durable progress: %v", f.requested)
	}
	detail, err := store.ReadConversation(t.Context(), sourceKey("delta"), 100, 0)
	if err != nil || len(detail.Messages) != 2 || len(detail.Parts) != 2 || detail.Parts[0].Text != "retained" || detail.Parts[1].Text != "new message" {
		t.Fatalf("incremental write damaged history: %+v %v", detail, err)
	}
	revision := detail.Session.Revision
	if _, err := restarted.importSession(t.Context(), f.entries[0]); err != nil {
		t.Fatal(err)
	}
	detail, err = store.ReadConversation(t.Context(), sourceKey("delta"), 100, 0)
	if err != nil || detail.Session.Revision != revision {
		t.Fatal("unchanged source cursor rewrote history")
	}
	if _, err := restarted.importSession(t.Context(), f.entries[0], true); err != nil {
		t.Fatal(err)
	}
	if f.requested[len(f.requested)-1] != "" {
		t.Fatal("explicit repair did not request a full snapshot")
	}
}

func TestFeedDuplicateEventDoesNotReadSource(t *testing.T) {
	f := newFakeFeed()
	m, _ := testImporter(t, f.fakeHistory)
	w := m.workers[0]
	w.source.Reader, w.feed = f, f
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	c := platforms.HistoryChange{SessionID: "session", Seq: 1, Resync: true}
	if err := w.applyChange(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	reads := f.reads
	if err := w.applyChange(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if f.reads != reads {
		t.Fatalf("replayed feed event reread history: %d -> %d", reads, f.reads)
	}
}

func TestFeedActivityDoesNotRereadAlreadyAppliedSequence(t *testing.T) {
	f := newFakeFeed()
	w, _ := feedImporter(t, f)
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := w.catchUpFeed(t.Context(), map[string]bool{}); err != nil {
		t.Fatal(err)
	}
	reads := f.reads
	if err := w.applyChanges(t.Context(), nil, map[string]bool{"session": false}); err != nil {
		t.Fatal(err)
	}
	if f.reads != reads {
		t.Fatalf("activity forced a full snapshot of an applied sequence: %d -> %d", reads, f.reads)
	}
}

func TestObserveDoesNotWeakenExplicitRepairs(t *testing.T) {
	m, _ := testImporter(t, fakeSource("source"))
	w := m.workers[0]
	m.Observe("source", "")
	if len(w.dirty) != 0 {
		t.Fatal("missing activity identity requested a source-wide repair")
	}
	m.Observe("source", "session")
	if forced, ok := w.dirty["session"]; !ok || forced {
		t.Fatal("activity hint became an explicit repair")
	}
	m.Notify("source", "session")
	m.Observe("source", "session")
	if !w.dirty["session"] {
		t.Fatal("activity hint weakened a pending explicit repair")
	}
}

func TestFailedTranscriptDeltaDoesNotAdvanceSourceCursor(t *testing.T) {
	f := &deltaHistory{fakeHistory: fakeSource("delta"), snapshot: historySnapshot("retained")}
	f.snapshot.SourceCursor = "offset-1"
	m, store := testImporter(t, f.fakeHistory)
	w := m.workers[0]
	w.source.Reader, w.journaled = f, true
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := w.importSession(t.Context(), f.entries[0]); err != nil {
		t.Fatal(err)
	}
	before, err := store.Checkpoint(t.Context(), w.instance.ID, snapshotStream("session"))
	if err != nil {
		t.Fatal(err)
	}
	f.snapshot = historySnapshot("must not publish")
	f.snapshot.Partial, f.snapshot.SourceCursor = true, "offset-2"
	// Malformed source content cannot advance either source progress or history.
	f.snapshot.Messages = append(f.snapshot.Messages, f.snapshot.Messages[0])
	if _, err := w.importSession(t.Context(), f.entries[0]); err == nil {
		t.Fatal("invalid delta accepted")
	}
	after, err := store.Checkpoint(t.Context(), w.instance.ID, snapshotStream("session"))
	if err != nil || after != before {
		t.Fatalf("failed delta advanced the checkpoint: %q -> %q: %v", before, after, err)
	}
	if text, _ := importText(t, store, sourceKey("delta")); text != "retained" {
		t.Fatal("failed delta changed retained history")
	}
	f.snapshot = historySnapshot("retry")
	f.snapshot.Partial, f.snapshot.SourceCursor = true, "offset-2"
	if _, err := w.importSession(t.Context(), f.entries[0]); err != nil {
		t.Fatal(err)
	}
	if f.requested[len(f.requested)-1] != "offset-1" {
		t.Fatal("retry skipped uncommitted source records")
	}
}
