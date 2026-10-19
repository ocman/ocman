package conversationingest

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestLiveSessionUpdateIsNotRecordedAsTask(t *testing.T) {
	f := fakeSource("first")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// A session being written changes while the source keeps its hint moving.
	f.snapshots["session"] = historySnapshot("live update")
	f.snapshots["session"].Version = "v2"
	f.entries[0].Version = "v2"
	if err := w.runPass(t.Context(), map[string]bool{"session": true}); err != nil {
		t.Fatal(err)
	}
	if tasks := listTasks(t, manager, "", ""); len(tasks) != 1 {
		t.Fatalf("live single-session update recorded a task each poll: %d tasks", len(tasks))
	}
}

func TestSingleSessionFailureIsStillRecorded(t *testing.T) {
	f := fakeSource("first")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.snapshotErr = errors.New("source read failed")
	f.snapshots["session"] = historySnapshot("changed")
	f.entries[0].Version = "v2"
	for range 3 {
		if err := w.runPass(t.Context(), nil); err == nil {
			t.Fatal("failed single-session read looked successful")
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("repeated single-session failure not recorded exactly once: %d tasks", len(tasks))
	}
	// New work alongside the broken session is still recorded.
	addSession(f, "new", "v1", "new")
	if err := w.runPass(t.Context(), nil); err == nil {
		t.Fatal("failed pass looked successful")
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 2 {
		t.Fatalf("new import alongside a broken session was not recorded: %d tasks", len(tasks))
	}
}

func TestRepeatedFailuresOfSeveralSessionsAreRecordedOnce(t *testing.T) {
	f := fakeSource("first")
	addSession(f, "other", "v1", "other")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.snapshotErr = errors.New("source read failed")
	f.entries[0].Version, f.entries[1].Version = "v2", "v2"
	for range 3 {
		if err := w.runPass(t.Context(), nil); err == nil {
			t.Fatal("failed reads looked successful")
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("repeated multi-session failure recorded %d tasks, want 1", len(tasks))
	}
}

func TestInitialImportFailureIsRecordedOnce(t *testing.T) {
	f := fakeSource("first")
	f.snapshotErr = errors.New("transcript is corrupt")
	m, store := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	for range 3 {
		if err := w.runPass(t.Context(), nil); err == nil {
			t.Fatal("failed initial import looked successful")
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("repeated initial-import failure recorded %d tasks, want 1", len(tasks))
	}
	f.snapshotErr = nil
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if text, _ := importText(t, store, sourceKey("first")); text != "first" {
		t.Fatalf("recovered session not imported: %q", text)
	}
}

func TestActiveSessionIsDeferredNotFailed(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	// A session written continuously fails every attempt within one pass.
	f.snapshots["session"] = historySnapshot("while writing")
	f.snapshots["session"].Version = "v2"
	f.entries[0].Version = "v2"
	f.changingReads = 100
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatalf("an actively written session failed the pass: %v", err)
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 0 {
		t.Fatalf("deferred session recorded as failure: %+v", tasks)
	}
	f.changingReads = 0
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if text, _ := importText(t, store, sourceKey("first")); text != "while writing" {
		t.Fatalf("deferred session not imported on the next poll: %q", text)
	}
}

func TestChangingSnapshotIsRetriedWithoutFailing(t *testing.T) {
	f := fakeSource("first")
	m, store := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.snapshots["session"] = historySnapshot("settled")
	f.entries[0].Version = "v2"
	f.snapshots["session"].Version = "v2"
	f.changingReads = 2
	if err := w.runPass(t.Context(), map[string]bool{"session": true}); err != nil {
		t.Fatalf("transient snapshot change failed the pass: %v", err)
	}
	if text, _ := importText(t, store, sourceKey("first")); text != "settled" {
		t.Fatalf("settled snapshot not imported: %q", text)
	}
	if len(listTasks(t, manager, tasksvc.Failed, syncTaskKind)) != 0 {
		t.Fatal("transient snapshot change recorded as failure")
	}
}
