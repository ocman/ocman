package conversationingest

import (
	"testing"
	"time"

	"github.com/ocman/ocman/internal/historyjournal"
	"github.com/ocman/ocman/internal/statev2"
)

func TestSubagentStopDoesNotRereadUnrelatedHistory(t *testing.T) {
	f := bulkHistory(190)
	m, _ := testImporter(t, f)
	m.backfill = newBackfillLimiter(100)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	manager := taskManager(t, m)
	for range 2 {
		before := f.reads
		if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session-0", Type: "SubagentStop"}}); err != nil {
			t.Fatal(err)
		}
		if reads := f.reads - before; reads != 1 {
			t.Fatalf("SubagentStop processed %d sessions, want only its parent; counters: %v", reads, w.last.Counters)
		}
	}
	if tasks := listTasks(t, manager, "", ""); len(tasks) != 0 {
		t.Fatalf("routine no-change hooks created %d background tasks", len(tasks))
	}
}

func TestSubagentStopDiscoversOnlyNewAndChangedSessions(t *testing.T) {
	f := bulkHistory(3)
	addSession(f, "child", "v1", "old result")
	f.snapshots["child"].Session.ParentID = "session-0"
	m, store := testImporter(t, f)
	m.backfill = newBackfillLimiter(100)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	addSession(f, "child", "v2", "changed result")
	f.snapshots["child"].Session.ParentID = "session-0"
	addSession(f, "new-child", "v1", "new result")
	f.snapshots["new-child"].Session.ParentID = "session-0"
	before := f.reads
	if err := w.refreshJournal(t.Context(), []historyjournal.Record{{Session: "session-0", Type: "SubagentStop"}}); err != nil {
		t.Fatal(err)
	}
	if reads := f.reads - before; reads != 3 || w.last.Counters["skipped"] != 2 {
		t.Fatalf("discovery read unrelated sessions: reads=%d counters=%v", reads, w.last.Counters)
	}
	for id, text := range map[string]string{"child": "changed result", "new-child": "new result"} {
		key := statev2.Key{InstanceID: w.instance.ID, SessionID: id}
		if got, _ := importText(t, store, key); got != text {
			t.Fatalf("child %s content = %q, want %q", id, got, text)
		}
	}
}

func TestSubagentStopPreservesExplicitSourceRepair(t *testing.T) {
	f := bulkHistory(3)
	m, _ := testImporter(t, f)
	m.backfill = newBackfillLimiter(100)
	w := m.workers[0]
	w.journaled = true
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := f.reads
	// Explicit repairs remain distinct from unresolved hooks' discovery scans.
	m.Notify("bulk", "")
	if err := w.sweepChecked(t.Context()); err != nil {
		t.Fatal(err)
	}
	if reads := f.reads - before; reads != 3 {
		t.Fatalf("source-wide repair read %d sessions, want 3", reads)
	}
}

func TestJournalSubagentStopAndActivityStayScoped(t *testing.T) {
	const sessions = 3
	f := bulkHistory(sessions)
	m, store := testImporter(t, f)
	m.backfill = newBackfillLimiter(100)
	m.deps.PollInterval = time.Second
	src := &journalSource{fakeHistory: f, dir: t.TempDir()}
	w := m.workers[0]
	w.source.Reader = src
	manager := taskManager(t, m)
	stop := runModule(t, m)
	defer stop()
	eventually(t, "startup import did not finish", func() bool { _, ready := m.ReadyInstances(); return ready })
	if err := historyjournal.Append(src.dir, "hooks.jsonl", historyjournal.Record{Session: "session-0", Type: "SubagentStop"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "subagent hook was not consumed", func() bool {
		cursor, _ := store.Checkpoint(t.Context(), w.instance.ID, journalStream("hooks.jsonl"))
		return cursor != ""
	})
	m.Observe("bulk", "session-0")
	eventually(t, "activity did not refresh its session", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.reads >= sessions+2
	})
	stop()
	if f.reads != sessions+2 {
		t.Fatalf("hook and activity read unrelated history: %d reads, want %d startup + 2 targeted", f.reads, sessions)
	}
	if tasks := listTasks(t, manager, "", ""); len(tasks) != 1 {
		t.Fatalf("routine hooks created extra tasks: got %d, want only startup", len(tasks))
	}
}
