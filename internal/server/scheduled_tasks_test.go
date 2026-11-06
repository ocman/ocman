package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestPreparationLifetimeObservesScheduledHistoryMaintenance(t *testing.T) {
	checked := make(chan error, 1)
	s := testServer(t).WithSessionPreparation(func(ctx context.Context) error {
		checked <- module.RunScheduled(ctx, module.Schedule{Name: "history-journal-prune.test", Title: "Prune history journal", Interval: time.Hour}, func(context.Context) error { return nil })
		return errors.New("stop before session consumers start")
	})
	_, cancel, done := startPreparationTestServer(t, s)
	defer func() { cancel(); joinPreparationTestServer(t, done) }()
	select {
	case err := <-checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preparation never ran")
	}
	rows, err := s.taskManager().List(t.Context(), "", "schedule.history-journal-prune.test", 10)
	if err != nil || len(rows) != 1 || rows[0].Schedule == nil {
		t.Fatalf("preparation lifetime lost schedule observer: %+v %v", rows, err)
	}
}

func TestServerMaintenanceModulesPublishRecurringTasks(t *testing.T) {
	core := statetest.Open(t, ModuleSchemas()...)
	s := New(nil, core, "127.0.0.1:0", platforms.NewRegistry(), nil)
	ctx, cancel := context.WithCancel(t.Context())
	ctx = module.WithScheduleObserver(ctx, s.taskManager())
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	if err := startModules(ctx, []module.Module{s.backgroundTasksModule(), s.worktreeCleanupModule(), s.snoozeModule()}, &workers); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		rows, err := s.taskManager().List(t.Context(), "succeeded", "", 20)
		if err != nil {
			t.Fatal(err)
		}
		seen := map[string]bool{}
		for _, row := range rows {
			if row.Schedule != nil && row.Schedule.Runs == 1 {
				seen[row.Kind] = true
			}
		}
		if seen["schedule.worktree-cleanup"] && seen["schedule.snooze"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("maintenance receipts missing: %+v", rows)
		}
		time.Sleep(time.Millisecond)
	}
	mux := http.NewServeMux()
	s.backgroundTasksModule().Routes(moduletest.Router{Mux: mux})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks?kind=schedule.worktree-cleanup", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("task API: %d %s", w.Code, w.Body)
	}
	if count, err := s.taskManager().MaintenanceBlockers(t.Context()); err != nil || count != 0 {
		t.Fatalf("observations block cleanup: %d %v", count, err)
	}
}
