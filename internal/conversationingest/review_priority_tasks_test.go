package conversationingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestCoalescedPriorityRequestsAllBypassLongBackfillCooldown(t *testing.T) {
	f := bulkHistory(3)
	m, _ := testImporter(t, f)
	w := m.workers[0]
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, entry := range f.entries {
		m.requestRead("bulk", entry.ID)
	}
	if len(w.wake) != 1 {
		t.Fatal("fixture did not coalesce foreground requests into one wake")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	served := 0
	attend := func() {
		if id := w.servePriority(ctx); id != "" {
			served++
		}
		if served == len(f.entries) {
			cancel()
		}
	}
	attend() // the direct read at the next background boundary
	m.backfill.next = time.Now().Add(time.Hour)
	m.backfill.slot <- struct{}{}
	if _, err := m.backfill.cooldown(ctx, w.wake, attend); !errors.Is(err, context.Canceled) {
		t.Fatalf("priority reads did not drain before the deadline: %v", err)
	}
	if served != len(f.entries) {
		t.Fatalf("foreground reads stranded behind cooldown: served %d of %d", served, len(f.entries))
	}
}

func TestDeferredOnlyInventoryRetriesRecordOneFailureUntilItChanges(t *testing.T) {
	f := fakeSource("source")
	f.inventoryErr = &platforms.HistoryDeferred{Count: 1}
	m, _ := testImporter(t, f)
	manager := taskManager(t, m)
	w := m.workers[0]
	for range 4 {
		if err := w.runPass(t.Context(), nil); err == nil {
			t.Fatal("incomplete inventory reported success")
		}
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("identical deferred retries created %d failed tasks, want 1", len(tasks))
	}
	f.inventoryErr = &platforms.HistoryDeferred{Count: 2}
	_ = w.runPass(t.Context(), nil)
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 1 {
		t.Fatalf("the same deferred failure created another task when its counter changed: %d", len(tasks))
	}
	f.inventoryErr = errors.New("source became unavailable")
	_ = w.runPass(t.Context(), nil)
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 2 {
		t.Fatalf("changed deferred failure was hidden: %d tasks", len(tasks))
	}
	f.inventoryErr = nil
	if err := w.runPass(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.inventoryErr = &platforms.HistoryDeferred{Count: 2}
	_ = w.runPass(t.Context(), nil)
	if tasks := listTasks(t, manager, tasksvc.Failed, syncTaskKind); len(tasks) != 3 {
		t.Fatalf("deferred failure recurring after recovery was hidden: %d tasks", len(tasks))
	}
	if err := w.runPrepared(t.Context(), pass{verify: true, deferred: 2}); err == nil {
		t.Fatal("explicit verification hid incomplete history")
	}
	if tasks := listTasks(t, manager, tasksvc.Failed, verifyTaskKind); len(tasks) != 1 {
		t.Fatal("explicit verification was deduplicated as an automatic retry")
	}
}
