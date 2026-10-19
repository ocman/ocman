package conversationingest

import (
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func taskManager(t *testing.T, m *Module) *tasksvc.Manager {
	t.Helper()
	d, err := state.Open(statetest.Path(t, tasksvc.Schema), tasksvc.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	manager := tasksvc.New(d, 2)
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	m.deps.Tasks = manager
	return manager
}

func listTasks(t *testing.T, manager *tasksvc.Manager, status tasksvc.Status, kind string) []tasksvc.Task {
	t.Helper()
	tasks, err := manager.List(t.Context(), status, kind, 200)
	if err != nil {
		t.Fatal(err)
	}
	return tasks
}

func TestManagedHistoryProgressIsQueryableAndFailureIsExplicit(t *testing.T) {
	f := fakeSource("first")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	tasks := listTasks(t, manager, "", "")
	if len(tasks) != 1 || tasks[0].Status != tasksvc.Succeeded || tasks[0].Progress.Completed != 1 || *tasks[0].Progress.Total != 1 || tasks[0].Progress.Counters["imported"] != 1 {
		t.Fatalf("initial progress: %+v", tasks)
	}
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(listTasks(t, manager, "", "")) != 1 {
		t.Fatal("an idle poll was recorded as a task")
	}
	f.snapshotErr = errors.New("source read failed")
	if err := w.runPass(t.Context(), map[string]bool{"session": true}); err == nil {
		t.Fatal("failed import looked successful")
	}
	tasks = listTasks(t, manager, tasksvc.Failed, syncTaskKind)
	if len(tasks) != 1 || tasks[0].Progress.Counters["failed"] != 1 || tasks[0].Progress.Completed != 1 || tasks[0].Error == "" {
		t.Fatalf("failure progress: %+v", tasks)
	}
	manager.Stop()
	w.verifiedAt = time.Time{} // exercise admission of a managed verification task
	if err := w.runPass(t.Context(), map[string]bool{"session": true}); !errors.Is(err, tasksvc.ErrClosed) {
		t.Fatal("late repair admitted", err)
	}
}

func TestRepeatedInventoryFailureIsRecordedOnce(t *testing.T) {
	f := fakeSource("first")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	f.inventoryErr = errors.New("database unavailable")
	for range 3 {
		if err := w.runPass(t.Context(), nil); err == nil {
			t.Fatal("failed inventory looked successful")
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 || tasks[0].Error == "" {
		t.Fatalf("repeated failure: %+v", tasks)
	}
	f.inventoryErr = errors.New("different failure")
	_ = w.runPass(t.Context(), nil)
	f.inventoryErr = nil
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.inventoryErr = errors.New("different failure")
	_ = w.runPass(t.Context(), nil)
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 3 {
		t.Fatalf("new or recurring failures hidden: %d", len(tasks))
	}
}

// addSession lists a session in the fake source with the given content.
func addSession(f *fakeHistory, id, version, text string) {
	snapshot := historySnapshot(text)
	snapshot.Session.ID, snapshot.Version = id, version
	snapshot.Messages[0].SessionID, snapshot.Parts[0].SessionID = id, id
	for i := range f.entries {
		if f.entries[i].ID == id {
			f.entries[i].Version = version
			f.snapshots[id] = snapshot
			return
		}
	}
	f.entries = append(f.entries, db.HistoryEntry{ID: id, Version: version})
	f.snapshots[id] = snapshot
}

func TestVerificationReportsDrift(t *testing.T) {
	f := fakeSource("first")
	addSession(f, "other", "v1", "other")
	addSession(f, "changed", "v1", "changed")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// Same hint, different content: a change normal passes cannot see.
	addSession(f, "session", "v1", "drifted")
	// A new hint read during verification is a normal change, not drift.
	addSession(f, "changed", "v2", "changed again")
	// A session the worker never stored.
	addSession(f, "new", "v1", "new")
	w.verifiedAt = time.Time{}
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	tasks := listTasks(t, manager, tasksvc.Succeeded, verifyTaskKind)
	if len(tasks) != 1 {
		t.Fatalf("verification task: %+v", tasks)
	}
	got := tasks[0].Progress.Counters
	if got["drifted"] != 1 || got["matched"] != 1 || got["changed"] != 1 || got["missing"] != 1 || tasks[0].Progress.Completed != 4 {
		t.Fatalf("verification counters: %+v", got)
	}
}
