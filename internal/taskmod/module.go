// Package taskmod exposes the task service without allowing clients to execute
// arbitrary work. Task submission remains an internal, typed operation.
package taskmod

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/tasksvc"
)

type Module struct {
	manager *tasksvc.Manager
	owners  func() map[string]tasksvc.Reader
}

func New(manager *tasksvc.Manager) *Module {
	return &Module{manager: manager}
}

// UseOwners supplies currently connected owner readers at call time. With no
// explicit owner the list/count includes local and connected remote work.
func (m *Module) UseOwners(owners func() map[string]tasksvc.Reader) *Module {
	m.owners = owners
	return m
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

func (m *Module) readers(w http.ResponseWriter, r *http.Request) map[string]tasksvc.Reader {
	readers := map[string]tasksvc.Reader{"local": m.manager}
	if m.owners != nil {
		for owner, reader := range m.owners() {
			if owner != "" && owner != "local" && reader != nil {
				readers[owner] = reader
			}
		}
	}
	owner := r.URL.Query().Get("remoteId")
	if owner == "" {
		return readers
	}
	reader, ok := readers[owner]
	if !ok {
		http.Error(w, "background task owner unavailable", http.StatusServiceUnavailable)
		return nil
	}
	return map[string]tasksvc.Reader{owner: reader}
}

func (m *Module) list(w http.ResponseWriter, r *http.Request) {
	readers := m.readers(w, r)
	if readers == nil {
		return
	}
	if r.URL.Query().Get("view") == "active-count" {
		count := 0
		for owner, reader := range readers {
			value, err := reader.ActiveCount(r.Context())
			if err != nil {
				ownerTaskError(w, owner, err)
				return
			}
			count += value
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
	tasks := []tasksvc.Task{}
	for owner, reader := range readers {
		rows, err := reader.List(r.Context(), status, r.URL.Query().Get("kind"), limit)
		if err != nil {
			ownerTaskError(w, owner, err)
			return
		}
		for i := range rows {
			rows[i].OwnerID = owner
		}
		tasks = append(tasks, rows...)
	}
	sort.Slice(tasks, func(i, j int) bool {
		rank := func(s tasksvc.Status) int {
			switch s {
			case tasksvc.Running:
				return 0
			case tasksvc.Queued:
				return 1
			default:
				return 2
			}
		}
		if a, b := rank(tasks[i].Status), rank(tasks[j].Status); a != b {
			return a < b
		}
		if tasks[i].UpdatedAt != tasks[j].UpdatedAt {
			return tasks[i].UpdatedAt > tasks[j].UpdatedAt
		}
		if tasks[i].ID != tasks[j].ID {
			return tasks[i].ID > tasks[j].ID
		}
		return tasks[i].OwnerID < tasks[j].OwnerID
	})
	if len(tasks) > limit {
		tasks = tasks[:limit]
	}
	httpx.WriteJSON(w, tasks)
}

func (m *Module) get(w http.ResponseWriter, r *http.Request) {
	// An unqualified ID retains the existing local lookup. Remote task IDs
	// must carry an explicit owner, since IDs are not globally unique.
	if r.URL.Query().Get("remoteId") == "" {
		q := r.URL.Query()
		q.Set("remoteId", "local")
		r.URL.RawQuery = q.Encode()
	}
	readers := m.readers(w, r)
	if readers == nil {
		return
	}
	owner := r.URL.Query().Get("remoteId")
	task, err := readers[owner].Get(r.Context(), r.PathValue("id"))
	if err != nil {
		ownerTaskError(w, owner, err)
		return
	}
	task.OwnerID = owner
	httpx.WriteJSON(w, task)
}

func ownerTaskError(w http.ResponseWriter, owner string, err error) {
	if owner != "local" && !errors.Is(err, tasksvc.ErrNotFound) {
		http.Error(w, "background task owner unavailable", http.StatusServiceUnavailable)
		return
	}
	taskError(w, err)
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
