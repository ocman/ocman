package taskmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
)

func TestRecurringMetadataAndPrioritySurviveOwnerAggregation(t *testing.T) {
	manager := func() *tasksvc.Manager {
		m := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
		if err := m.Start(t.Context()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(m.Stop)
		return m
	}
	local, remote := manager(), manager()
	if err := remote.ObserveSchedule(t.Context(), module.Schedule{Name: "cleanup", Title: "Clean archived worktrees", Scope: "public scope", Interval: time.Hour}, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := local.Do(t.Context(), tasksvc.Spec{Kind: "scan", Title: "Recent scan"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	m := New(local).UseOwners(func() map[string]tasksvc.Reader { return map[string]tasksvc.Reader{"workstation": remote} })
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks?limit=1", nil))
	var rows []tasksvc.Task
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil || w.Code != 200 || len(rows) != 1 {
		t.Fatalf("response: %s %v", w.Body, err)
	}
	row := rows[0]
	if row.OwnerID != "workstation" || row.Schedule == nil || row.Schedule.Runs != 1 || row.Schedule.IntervalMillis != time.Hour.Milliseconds() || row.Scope != "public scope" {
		t.Fatalf("schedule lost: %+v", row)
	}
}
