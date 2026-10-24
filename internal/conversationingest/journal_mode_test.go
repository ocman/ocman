package conversationingest

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// journalSource is a polled source whose hooks write a history journal.
type journalSource struct {
	*fakeHistory
	dir string
}

func (j *journalSource) HistoryJournalDir() string { return j.dir }

func journalModule(t *testing.T, poll time.Duration) (*Module, *journalSource, *statev2.DB) {
	t.Helper()
	store, err := statev2.Open(t.TempDir() + "/statev2.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	src := &journalSource{fakeHistory: fakeSource("claude"), dir: t.TempDir()}
	m := NewModule(Deps{Store: store, OwnerID: "owner", PollInterval: poll, Debounce: time.Millisecond, ReadTimeout: time.Second,
		VerifyInterval: time.Nanosecond, Sources: []Source{{AdapterID: "claude", Reader: src}}})
	return m, src, store
}

func runModule(t *testing.T, m *Module) func() {
	t.Helper()
	var done sync.WaitGroup
	ctx, cancel := context.WithCancel(t.Context())
	done.Go(func() { m.Run(ctx) })
	return func() { cancel(); done.Wait() }
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

func TestJournalModeRefreshesOnHookRecordsWithoutPolling(t *testing.T) {
	m, src, store := journalModule(t, 20*time.Millisecond)
	stop := runModule(t, m)
	defer stop()
	eventually(t, "startup import did not finish", func() bool { _, ready := m.ReadyInstances(); return ready })
	key := sourceKey("claude")
	if text, _ := importText(t, store, key); text != "claude" {
		t.Fatalf("startup import = %q", text)
	}
	// Many poll intervals pass: neither inventory-wide verification nor
	// periodic rereads run while nothing is journaled.
	src.mu.Lock()
	reads := src.reads
	src.mu.Unlock()
	time.Sleep(120 * time.Millisecond)
	src.mu.Lock()
	idle := src.reads
	src.mu.Unlock()
	if idle != reads {
		t.Fatalf("journal source polled %d snapshots while idle", idle-reads)
	}
	// A transcript changes without a new version hint; the hook record alone
	// makes ingestion reread exactly that session.
	src.mu.Lock()
	src.snapshots["session"] = historySnapshot("from a terminal session")
	src.mu.Unlock()
	if err := historyjournal.Append(src.dir, "claudecode-20261020.jsonl", historyjournal.Record{Session: "session", Type: "Stop"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "hook record was not applied", func() bool {
		detail, err := store.ReadConversation(t.Context(), key, 10, 0)
		return err == nil && len(detail.Parts) == 1 && detail.Parts[0].Text == "from a terminal session"
	})
	eventually(t, "journal offset did not advance", func() bool {
		cursor, _ := store.Checkpoint(t.Context(), key.InstanceID, journalStream("claudecode-20261020.jsonl"))
		return cursor != ""
	})
	eventually(t, "journal source did not stay ready", func() bool { _, ready := m.ReadyInstances(); return ready })
}

func TestJournalModeCatchesUpChangesMadeWhileStopped(t *testing.T) {
	m, src, store := journalModule(t, time.Hour)
	stop := runModule(t, m)
	eventually(t, "startup import did not finish", func() bool { _, ready := m.ReadyInstances(); return ready })
	stop()
	// While ocman is stopped the transcript changes and its hook records it.
	src.snapshots["session"] = historySnapshot("changed while stopped")
	src.entries[0].Version = "v2"
	src.snapshots["session"].Version = "v2"
	restarted := NewModule(m.deps)
	stop = runModule(t, restarted)
	defer stop()
	key := sourceKey("claude")
	eventually(t, "restart did not catch up", func() bool {
		detail, err := store.ReadConversation(t.Context(), key, 10, 0)
		return err == nil && len(detail.Parts) == 1 && detail.Parts[0].Text == "changed while stopped"
	})
}
