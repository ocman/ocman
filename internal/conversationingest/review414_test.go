package conversationingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestQuietFeedRetriesBeforePoll(t *testing.T) {
	f := newFakeFeed()
	w, _ := feedImporter(t, f)
	stop := runModule(t, w.module)
	defer stop()
	eventually(t, "feed did not connect", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.connects > 0
	})
	eventually(t, "initial import not ready", func() bool { _, ready := w.module.ReadyInstances(); return ready })
	f.mu.Lock()
	f.messageErr = errors.New("offline")
	f.sequences["session"] = 4
	f.mu.Unlock()
	f.changes <- platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}}
	eventually(t, "live read did not fail", func() bool {
		f.mu.Lock()
		read := len(f.messageReads) > 0
		f.mu.Unlock()
		_, ready := w.module.ReadyInstances()
		return read && !ready
	})
	f.mu.Lock()
	f.messageErr = nil
	f.mu.Unlock()
	eventually(t, "quiet feed never retried", func() bool { _, ready := w.module.ReadyInstances(); return ready })
}

func TestFeedRetiresDirtyDeletedSession(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.messageErr = errors.New("offline")
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{"message"}})
	w.drainFeed(t.Context())
	f.entries = nil
	delete(f.sequences, "session")
	f.snapshotErr = errors.New("not found")
	w.drainFeed(t.Context())
	s, _, err := store.SessionState(t.Context(), feedKey(w))
	_, ready := w.module.ReadyInstances()
	if err != nil || !s.SourceDeleted || !ready || len(w.dirty) != 0 {
		t.Fatalf("deleted repair did not retire: session=%+v ready=%v dirty=%v err=%v", s, ready, w.dirty, err)
	}
}

func TestSnapshotAfterIncrementalMutationRestoresOriginal(t *testing.T) {
	f := newFakeFeed()
	w, store := feedImporter(t, f)
	w.drainFeed(t.Context())
	f.snapshots["session"] = historySnapshot("incremental B")
	f.sequences["session"] = 4
	w.enqueue(platforms.HistoryChange{SessionID: "session", Seq: 4, MessageIDs: []string{f.snapshots["session"].Messages[0].ID}})
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "incremental B" {
		t.Fatal(text)
	}
	f.snapshots["session"] = historySnapshot("feed")
	w.module.Notify("feed", "session")
	w.drainFeed(t.Context())
	if text, _ := importText(t, store, feedKey(w)); text != "feed" {
		t.Fatalf("stale snapshot digest accepted incremental content: %q", text)
	}
}

func TestJournalTranslationFailureHasOneReceipt(t *testing.T) {
	m, src, store := journalModule(t, 5*time.Millisecond)
	w := m.workers[0]
	w.journaled = true
	w.sweep(t.Context())
	manager := taskManager(t, m)
	src.snapshotErr = errors.New("offline")
	if err := historyjournal.Append(src.dir, "hook.jsonl", historyjournal.Record{Session: "session", Type: "Stop"}); err != nil {
		t.Fatal(err)
	}
	consumer := &journalConsumer{store: store, dir: src.dir, instanceID: w.instance.ID, translator: journalRefresh{w}}
	for range 4 {
		_, err := consumer.drain(context.Background())
		if err == nil {
			t.Fatal("expected translation failure")
		}
		// Use the same failure admission as the journal loop.
		var translation *journalTranslationError
		if !errors.As(err, &translation) {
			_ = w.recordFailure(t.Context(), err, w.last)
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("identical translation failures created %d tasks", len(tasks))
	}
}
