package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
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
		serverError(w, "reading project settings", err)
		return
	}
	writeJSON(w, ps)
}

func (s *Server) postProjectSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string `json:"directory"`
		state.ProjectSettings
	}
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return
	}
	dir := strings.TrimSpace(req.Directory)
	if dir == "" {
		http.Error(w, "directory is required", http.StatusBadRequest)
		return
	}
	if err := s.stateDB.SetProjectSettings(r.Context(), dir, req.ProjectSettings); err != nil {
		if errors.Is(err, state.ErrInvalidProjectSettings) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		serverError(w, "saving project settings", err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
