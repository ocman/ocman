package taskmod

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/module/moduletest"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/tasksvc"
)

type taskReader struct {
	tasks []tasksvc.Task
	err   error
}

func (r *taskReader) List(_ context.Context, state tasksvc.Status, kind string, limit int) ([]tasksvc.Task, error) {
	rows := []tasksvc.Task{}
	for _, task := range r.tasks {
		if (state == "" || task.Status == state) && (kind == "" || task.Kind == kind) {
			rows = append(rows, task)
		}
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, r.err
}
func (r *taskReader) Get(_ context.Context, id string) (tasksvc.Task, error) {
	if r.err != nil {
		return tasksvc.Task{}, r.err
	}
	for _, task := range r.tasks {
		if task.ID == id {
			return task, nil
		}
	}
	return tasksvc.Task{}, tasksvc.ErrNotFound
}
func (r *taskReader) ActiveCount(context.Context) (int, error) {
	count := 0
	for _, task := range r.tasks {
		if task.Status == tasksvc.Running || task.Status == tasksvc.Queued {
			count++
		}
	}
	return count, r.err
}

func TestBackgroundTasksAggregateOwnersAndRouteExplicitReads(t *testing.T) {
	manager := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
	if err := manager.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(manager.Stop)
	if err := manager.Do(t.Context(), tasksvc.Spec{Kind: "scan", Title: "Local scan"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	remote := &taskReader{tasks: []tasksvc.Task{
		{ID: "running", Spec: tasksvc.Spec{Kind: "scan", Title: "Remote scan"}, Status: tasksvc.Running, UpdatedAt: 10, Progress: tasksvc.Progress{Completed: 3}},
		{ID: "queued", Status: tasksvc.Queued, UpdatedAt: 20},
		{ID: "done", Status: tasksvc.Succeeded, UpdatedAt: 1},
	}}
	m := New(manager).UseOwners(func() map[string]tasksvc.Reader { return map[string]tasksvc.Reader{"box": remote} })
	handler := moduletest.Mount(m)
	get := func(path string, status int) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks"+path, nil))
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return w
	}
	var tasks []tasksvc.Task
	w := get("?limit=2", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil || len(tasks) != 2 || tasks[0].ID != "running" || tasks[1].ID != "queued" || tasks[0].OwnerID != "box" || tasks[0].Progress.Completed != 3 {
		t.Fatalf("aggregate=%s err=%v", w.Body, err)
	}
	w = get("?view=active-count", 200)
	if w.Body.String() != "{\"count\":2}\n" {
		t.Fatal(w.Body.String())
	}
	w = get("?remoteId=local", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil || len(tasks) != 1 || tasks[0].OwnerID != "local" {
		t.Fatalf("local=%s", w.Body)
	}
	w = get("?remoteId=box&status=running&kind=scan", 200)
	if err := json.Unmarshal(w.Body.Bytes(), &tasks); err != nil || len(tasks) != 1 || tasks[0].ID != "running" {
		t.Fatalf("filtered=%s", w.Body)
	}
	get("/running?remoteId=box", 200)
	get("/missing?remoteId=box", 404)
	get("/running", 404)
	get("?remoteId=gone", 503)
	remote.err = errors.New("offline")
	get("?remoteId=box", 503)
	get("?view=active-count", 503)
	get("/running?remoteId=box", 503)
	get("", 503)
	get("?remoteId=local", 200)
}

func TestOwnerAggregationKeepsCollidingIDsAndStableOrdering(t *testing.T) {
	manager := tasksvc.New(statetest.Open(t, tasksvc.Schema), 1)
	box := &taskReader{tasks: []tasksvc.Task{{ID: "b", Status: tasksvc.Running, UpdatedAt: 10}, {ID: "a", Status: tasksvc.Running, UpdatedAt: 10}}}
	other := &taskReader{tasks: []tasksvc.Task{{ID: "b", Status: tasksvc.Running, UpdatedAt: 10}}}
	m := New(manager).UseOwners(func() map[string]tasksvc.Reader {
		return map[string]tasksvc.Reader{"": box, "local": box, "nil": nil, "box": box, "other": other}
	})
	w := httptest.NewRecorder()
	moduletest.Mount(m).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/background-tasks", nil))
	var tasks []tasksvc.Task
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tasks) != nil || len(tasks) != 3 {
		t.Fatalf("response=%d %s", w.Code, w.Body)
	}
	for i, want := range []string{"box:b", "other:b", "box:a"} {
		if got := tasks[i].OwnerID + ":" + tasks[i].ID; got != want {
			t.Fatalf("row %d = %s, want %s", i, got, want)
		}
	}
}
