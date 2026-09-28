package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"

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
	s.projectModels.forget(dir)
	writeJSON(w, map[string]bool{"ok": true})
}

// projectModelCache memoizes each project's model list so resolving the
// project default on a send costs no state.db round trip. The only
// writer, postProjectSettings, drops the entry it changes.
type projectModelCache struct {
	mu     sync.Mutex
	byRoot map[string][]string
}

func (c *projectModelCache) forget(dir string) {
	c.mu.Lock()
	delete(c.byRoot, state.ProjectRootForDirectory(dir))
	c.mu.Unlock()
}

// projectModelList returns dir's project model list (nil when none or
// unreadable). Read errors are not cached so a transient failure heals.
func (s *Server) projectModelList(ctx context.Context, dir string) []string {
	if s.stateDB == nil {
		return nil
	}
	root := state.ProjectRootForDirectory(dir)
	c := &s.projectModels
	// ponytail: the lock spans the miss read so a concurrent forget can't
	// be overwritten by a stale value; misses are rare once warm.
	c.mu.Lock()
	defer c.mu.Unlock()
	if models, ok := c.byRoot[root]; ok {
		return models
	}
	ps, err := s.stateDB.GetProjectSettings(ctx, root)
	if err != nil {
		return nil
	}
	if c.byRoot == nil {
		c.byRoot = map[string][]string{}
	}
	c.byRoot[root] = ps.Models
	return ps.Models
}

// projectDefaultModel is the first configured model for dir's project.
func (s *Server) projectDefaultModel(ctx context.Context, dir string) string {
	if models := s.projectModelList(ctx, dir); len(models) > 0 {
		return models[0]
	}
	return ""
}
