package taskmod

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

func TestQueryableTaskRoutesAreReadOnlyAndAuthenticated(t *testing.T) {
	d, err := state.Open(statetest.Path(t, tasksvc.Schema), tasksvc.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	manager := tasksvc.New(d, 1)
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-Auth") != "yes" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	m := New(manager)
	if m.Name() != "background-tasks" {
		t.Fatal(m.Name())
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := m.Start(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	if err := manager.Do(t.Context(), tasksvc.Spec{Kind: "test", Title: "Example"}, func(context.Context, *tasksvc.Reporter) error { return nil }); err != nil {
		t.Fatal(err)
	}
	tasks, err := manager.List(t.Context(), "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux, Auth: guard})
	for _, test := range []struct {
		method, path string
		auth         bool
		code         int
	}{
		{"GET", "/api/background-tasks", false, 401},
		{"GET", "/api/background-tasks?view=active-count", false, 401},
		{"GET", "/api/background-tasks?view=active-count&remoteId=offline", true, 503},
		{"GET", "/api/background-tasks", true, 200},
		{"GET", "/api/background-tasks?status=succeeded&kind=test&limit=1", true, 200},
		{"GET", "/api/background-tasks?status=invalid", true, 400},
		{"GET", "/api/background-tasks?limit=abc", true, 400},
		{"GET", "/api/background-tasks?limit=0", true, 400},
		{"GET", "/api/background-tasks?limit=201", true, 400},
		{"GET", "/api/background-tasks?remoteId=offline", true, 503},
		{"GET", "/api/background-tasks?remoteId=local", true, 200},
		{"GET", "/api/background-tasks/" + tasks[0].ID, true, 200},
		{"GET", "/api/background-tasks/missing", true, 404},
		{"GET", "/api/background-tasks/" + tasks[0].ID + "?remoteId=offline", true, 503},
		{"POST", "/api/background-tasks", true, 405},
	} {
		t.Run(test.path+test.method, func(t *testing.T) {
			r := httptest.NewRequest(test.method, test.path, nil)
			if test.auth {
				r.Header.Set("X-Test-Auth", "yes")
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
			if w.Code != test.code {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
		})
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/background-tasks", nil)
	r.Header.Set("X-Test-Auth", "yes")
	mux.ServeHTTP(w, r)
	var got []tasksvc.Task
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 1 || got[0].Status != tasksvc.Succeeded {
		t.Fatalf("task response: %+v %v", got, err)
	}
	d.Close()
	for _, path := range []string{"/api/background-tasks", "/api/background-tasks/unknown", "/api/background-tasks?view=active-count"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Test-Auth", "yes")
		mux.ServeHTTP(w, r)
		if w.Code != 500 {
			t.Fatalf("closed database returned %d", w.Code)
		}
	}
}

func TestUnavailableTaskManager(t *testing.T) {
	m := New(tasksvc.New(nil, 1))
	mux := http.NewServeMux()
	m.Routes(moduletest.Router{Mux: mux})
	for _, path := range []string{"/api/background-tasks", "/api/background-tasks/id", "/api/background-tasks?view=active-count"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	}
}

func TestCancelledStartupIsNotFatal(t *testing.T) {
	d, err := state.Open(statetest.Path(t, tasksvc.Schema), tasksvc.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	manager := tasksvc.New(d, 1)
	m := New(manager)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("shutdown during startup became fatal: %v", err)
	}
	m.Run(ctx)
}

func TestStartupDatabaseFailureIsStillFatal(t *testing.T) {
	d, err := state.Open(statetest.Path(t, tasksvc.Schema), tasksvc.Schema)
	if err != nil {
		t.Fatal(err)
	}
	d.Close()
	m := New(tasksvc.New(d, 1))
	if err := m.Start(t.Context()); err == nil {
		t.Fatal("startup hid a database failure")
	}
}
