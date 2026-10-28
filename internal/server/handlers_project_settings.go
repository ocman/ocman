package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
)

// handleProjectSettings serves a project's ordered model list, keyed by
// the folded project root (a worktree shares its repository's entry).
// Local-only: the list lives in the hub's state.db.
//
// GET  /api/project/settings?dir=<abs>
// POST /api/project/settings  { directory, models, off }
func (s *Server) handleProjectSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getProjectSettings(w, r)
	case http.MethodPost:
		s.postProjectSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getProjectSettings(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	ps, err := s.stateDB.GetProjectSettings(r.Context(), dir)
	if err != nil {
		httpx.ServerError(w, "reading project settings", err)
		return
	}
	agent, err := s.defaultAgent(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading default agent", err)
		return
	}
	defaults, err := s.getProjectDefaults(r.Context(), dir, r.URL.Query().Get("remoteId"))
	if err != nil {
		httpx.ServerError(w, "reading project defaults", err)
		return
	}
	if defaults != nil && defaults.Agent != "" {
		agent = defaults.Agent
	}
	httpx.WriteJSON(w, struct {
		state.ProjectSettings
		DefaultAgent string           `json:"defaultAgent"`
		Defaults     *projectDefaults `json:"defaults,omitempty"`
	}{ps, agent, defaults})
}

func (s *Server) postProjectSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string           `json:"directory"`
		RemoteID  string           `json:"remoteId"`
		Defaults  *projectDefaults `json:"defaults"`
		state.ProjectSettings
	}
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return
	}
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	if req.Defaults != nil {
		if err := req.Defaults.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, _ := json.Marshal(req.Defaults)
		if err := s.stateDB.SetSetting(r.Context(), projectDefaultsKey(dir, req.RemoteID), string(raw)); err != nil {
			httpx.ServerError(w, "saving project defaults", err)
			return
		}
		httpx.WriteJSON(w, map[string]bool{"ok": true})
		return
	}
	if err := s.stateDB.SetProjectSettings(r.Context(), dir, req.ProjectSettings); err != nil {
		if errors.Is(err, state.ErrInvalidProjectSettings) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		httpx.ServerError(w, "saving project settings", err)
		return
	}
	s.queueModule().ForgetProjectModels(dir)
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}

// projectModelList returns dir's project model list (nil when none or
// unreadable) and its fallthrough off switch. Read errors are not cached
// so a transient failure heals.
func (s *Server) projectModelList(ctx context.Context, dir string) ([]string, bool) {
	return s.queueModule().ProjectModelList(ctx, dir)
}

// projectDefaultModel is the first configured model for dir's project.
func (s *Server) projectDefaultModel(ctx context.Context, dir string) string {
	if models, _ := s.projectModelList(ctx, dir); len(models) > 0 {
		return models[0]
	}
	return ""
}
