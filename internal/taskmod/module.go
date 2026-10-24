// Package taskmod exposes the task service without allowing clients to execute
// arbitrary work. Task submission remains an internal, typed operation.
package taskmod

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/tasksvc"
)

type Module struct {
	manager *tasksvc.Manager
}

func New(manager *tasksvc.Manager) *Module {
	return &Module{manager: manager}
}
func (*Module) Name() string { return "background-tasks" }
func (m *Module) Start(ctx context.Context) error {
	err := m.manager.Start(ctx)
	// A signal may stop a reload before startup finishes. That is a normal
	// shutdown, not a database failure that should terminate the application.
	if ctx.Err() != nil && errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}
func (m *Module) Run(ctx context.Context) { <-ctx.Done(); m.manager.Stop() }

func (m *Module) Routes(router module.Router) {
	router.Handle("GET /api/background-tasks", m.list)
	router.Handle("GET /api/background-tasks/{id}", m.get)
}

func (m *Module) local(w http.ResponseWriter, r *http.Request) bool {
	if owner := r.URL.Query().Get("remoteId"); owner != "" && owner != "local" {
		http.Error(w, "background tasks for a remote owner are not available on this instance", http.StatusServiceUnavailable)
		return false
	}
	return true
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	if !m.local(w, r) {
		return
	}
	if r.URL.Query().Get("view") == "active-count" {
		count, err := m.manager.ActiveCount(r.Context())
		if err != nil {
			taskError(w, err)
			return
		}
		httpx.WriteJSON(w, struct {
			Count int `json:"count"`
		}{count})
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			http.Error(w, "limit must be between 1 and 200", http.StatusBadRequest)
			return
		}
		limit = parsed
	}
	status := tasksvc.Status(r.URL.Query().Get("status"))
	if status != "" && !tasksvc.ValidStatus(status) {
		http.Error(w, "invalid task status", http.StatusBadRequest)
		return
	}
	tasks, err := m.manager.List(r.Context(), status, r.URL.Query().Get("kind"), limit)
	if err != nil {
		taskError(w, err)
		return
	}
	httpx.WriteJSON(w, tasks)
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	if !m.local(w, r) {
		return
	}
	task, err := m.manager.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		taskError(w, err)
		return
	}
	httpx.WriteJSON(w, task)
}

func taskError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, tasksvc.ErrUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	case errors.Is(err, tasksvc.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	default:
		http.Error(w, "could not read background task status", http.StatusInternalServerError)
	}
}
