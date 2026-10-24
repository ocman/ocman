package conversationingest

import (
	"errors"
	"testing"
	"time"
)

func TestReadinessRequiresEverySourceToFinishCleanly(t *testing.T) {
	f, g := fakeSource("one"), fakeSource("two")
	m, _ := testImporter(t, f, g)
	if m.Name() != "conversation-ingestion" {
		t.Fatal(m.Name())
	}
	m.Routes(nil) // ingestion serves no HTTP routes
	assertReady := func(want bool) {
		t.Helper()
		ids, ready := m.ReadyInstances()
		if ready != want || (ready && len(ids) != 2) {
			t.Fatalf("ready=%v ids=%v, want %v", ready, ids, want)
		}
	}
	assertReady(false)
	m.workers[0].sweep(t.Context())
	assertReady(false)
	m.workers[1].sweep(t.Context())
	assertReady(true)
	g.inventoryErr = errors.New("disconnected")
	m.workers[1].sweep(t.Context())
	assertReady(false)
	g.inventoryErr = nil
	m.workers[1].sweep(t.Context())
	assertReady(true)
	m.readyMu.Lock()
	r := m.ready["one"]
	r.at = time.Now().Add(-3 * m.deps.PollInterval)
	m.ready["one"] = r
	m.readyMu.Unlock()
	if !m.LastCheckedAt().Equal(r.at) {
		t.Fatalf("oldest scan = %v, want %v", m.LastCheckedAt(), r.at)
	}
	assertReady(false)
	m.workers[0].sweep(t.Context())
	assertReady(true)
}

func TestDeferredSessionDoesNotEnableHistoryReads(t *testing.T) {
	f := fakeSource("one")
	f.changingReads = 100
	m, _ := testImporter(t, f)
	m.workers[0].sweep(t.Context())
	if _, ready := m.ReadyInstances(); ready {
		t.Fatal("partial import enabled reads")
	}
	f.changingReads = 0
	m.workers[0].sweep(t.Context())
	if _, ready := m.ReadyInstances(); !ready {
		t.Fatal("clean retry did not enable reads")
	}
}

func TestDeferredInvalidationMustBeRetriedEvenWithoutNewHint(t *testing.T) {
	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	w.sweep(t.Context())
	f.snapshots["session"] = historySnapshot("changed with same hint")
	f.changingReads = 100
	m.Notify("one", "session")
	w.sweep(t.Context())
	w.sweep(t.Context()) // poll without another event or version change
	if _, ready := m.ReadyInstances(); ready {
		t.Fatal("forgot a deferred invalidation and enabled stale history")
	}
	f.changingReads = 0
	w.sweep(t.Context())
	if text, _ := importText(t, store, sourceKey("one")); text != "changed with same hint" {
		t.Fatalf("lost repair: %q", text)
	}
}

func TestAccountingMismatchCannotEnableReadsOnNextIdlePoll(t *testing.T) {
	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	w.sweep(t.Context())
	batch, _, err := Normalize(sourceKey("one"), historySnapshot("one"))
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedRevision = 1
	batch.Messages[0].Cost = 42 // wrong normalized column, unchanged checkpoint
	if _, err := store.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	w.verifiedAt = time.Time{}
	w.sweep(t.Context())
	w.sweep(t.Context())
	if _, ready := m.ReadyInstances(); ready {
		t.Fatal("unresolved accounting mismatch enabled history reads")
	}
}

func TestDeferredRetriesDoNotCreateRoutineTasks(t *testing.T) {
	f := fakeSource("one")
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	w.sweep(t.Context())
	f.changingReads = 100
	m.Notify("one", "session")
	w.sweep(t.Context())
	w.sweep(t.Context())
	if tasks := listTasks(t, manager, "", ""); len(tasks) != 1 {
		t.Fatalf("routine deferred retries created %d tasks", len(tasks))
	}
}
