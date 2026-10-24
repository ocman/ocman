package conversationingest

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
)

func TestJournalRetriesStartupFailureWithoutNewHooks(t *testing.T) {
	m, src, store := journalModule(t, 20*time.Millisecond)
	src.inventoryErr = errors.New("temporarily unavailable")
	attempted := make(chan struct{}, 1)
	m.deps.OnError = func(_, _ string, _ error) {
		select {
		case attempted <- struct{}{}:
		default:
		}
	}
	stop := runModule(t, m)
	defer stop()
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("initial attempt did not run")
	}
	src.mu.Lock()
	src.inventoryErr = nil
	src.mu.Unlock()
	eventually(t, "startup failure was never retried without a hook", func() bool {
		d, err := store.ReadConversation(t.Context(), sourceKey("claude"), 10, 0)
		return err == nil && len(d.Parts) == 1
	})
}

func TestJournalDeferredSnapshotDoesNotConsumeHook(t *testing.T) {
	m, src, store := journalModule(t, 20*time.Millisecond)
	w := m.workers[0]
	w.journaled = true
	w.sweep(t.Context())
	src.changingReads = 3
	if err := historyjournal.Append(src.dir, "hook.jsonl", historyjournal.Record{Session: "session", Type: "Stop"}); err != nil {
		t.Fatal(err)
	}
	c := &journalConsumer{store: store, dir: src.dir, instanceID: w.instance.ID, refresh: w.refreshJournal}
	if _, err := c.drain(t.Context()); err == nil {
		t.Fatal("deferred snapshot consumed the final hook")
	}
	if cursor, _ := store.Checkpoint(t.Context(), w.instance.ID, journalStream("hook.jsonl")); cursor != "" {
		t.Fatalf("advanced cursor %s", cursor)
	}
	if _, err := c.drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestJournalReadFailureRevokesReadiness(t *testing.T) {
	m, src, _ := journalModule(t, 20*time.Millisecond)
	stop := runModule(t, m)
	defer stop()
	eventually(t, "not ready", func() bool { _, ok := m.ReadyInstances(); return ok })
	if err := os.Remove(src.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src.dir, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "journal read error renewed readiness", func() bool { _, ok := m.ReadyInstances(); return !ok })
	if err := os.Remove(src.dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(src.dir, 0700); err != nil {
		t.Fatal(err)
	}
	eventually(t, "journal error did not recover", func() bool { _, ok := m.ReadyInstances(); return ok })
}
