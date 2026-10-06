package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/sessionsvc"
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
	agent, err := s.defaultAgent(r.Context())
	if err != nil {
		serverError(w, "reading default agent", err)
		return
	}
	writeJSON(w, struct {
		state.ProjectSettings
		DefaultAgent string `json:"defaultAgent"`
	}{ps, agent})
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

// projectModelCache memoizes each project's settings so model selection
// on a send costs no state.db round trip. The only writer,
// postProjectSettings, drops the entry it changes.
type projectModelCache struct {
	mu     sync.Mutex
	byRoot map[string]state.ProjectSettings
}

func (c *projectModelCache) forget(dir string) {
	c.mu.Lock()
	delete(c.byRoot, state.ProjectRootForDirectory(dir))
	c.mu.Unlock()
}

// projectModelList returns dir's project model list (nil when none or
// unreadable) and its fallthrough off switch. Read errors are not cached
// so a transient failure heals.
func (s *Server) projectModelList(ctx context.Context, dir string) ([]string, bool) {
	if s.stateDB == nil {
		return nil, false
	}
	root := state.ProjectRootForDirectory(dir)
	c := &s.projectModels
	// ponytail: the lock spans the miss read so a concurrent forget can't
	// be overwritten by a stale value; misses are rare once warm.
	c.mu.Lock()
	defer c.mu.Unlock()
	ps, ok := c.byRoot[root]
	if !ok {
		var err error
		if ps, err = s.stateDB.GetProjectSettings(ctx, root); err != nil {
			return nil, false
		}
		if c.byRoot == nil {
			c.byRoot = map[string]state.ProjectSettings{}
		}
		c.byRoot[root] = ps
	}
	return ps.Models, ps.Off
}

// projectDefaultModel is the first configured model for dir's project.
func (s *Server) projectDefaultModel(ctx context.Context, dir string) string {
	if models, _ := s.projectModelList(ctx, dir); len(models) > 0 {
		return models[0]
	}
	return ""
}

const (
	modelFallthroughSettingKey = "model_fallthrough"
	maxFallthroughMinutes      = 24 * 60
)

// modelFallthroughSettings are the two global cooldown thresholds.
type modelFallthroughSettings struct {
	PatienceMinutes int `json:"patienceMinutes"`
	FallbackMinutes int `json:"fallbackMinutes"`
}

func (m modelFallthroughSettings) validate() error {
	if m.PatienceMinutes < 1 || m.PatienceMinutes > maxFallthroughMinutes ||
		m.FallbackMinutes < 1 || m.FallbackMinutes > maxFallthroughMinutes {
		return fmt.Errorf("minutes must be between 1 and %d", maxFallthroughMinutes)
	}
	return nil
}

func (s *Server) getModelFallthroughSettings(ctx context.Context) (modelFallthroughSettings, error) {
	m := modelFallthroughSettings{
		PatienceMinutes: int(sessionsvc.DefaultPatience / time.Minute),
		FallbackMinutes: int(sessionsvc.DefaultCooldownFallback / time.Minute),
	}
	if s.stateDB == nil {
		return m, errors.New("state database not available")
	}
	raw, ok, err := s.stateDB.GetSetting(ctx, modelFallthroughSettingKey)
	if err != nil || !ok {
		return m, err
	}
	var stored modelFallthroughSettings
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		return m, fmt.Errorf("decoding model fallthrough settings: %w", err)
	}
	if err := stored.validate(); err != nil {
		return m, err
	}
	return stored, nil
}

// cooldownTimes feeds sessionsvc.Hooks.CooldownTimes; an unreadable
// setting falls back to the defaults so detection never stalls.
func (s *Server) cooldownTimes(ctx context.Context) (time.Duration, time.Duration) {
	m, _ := s.getModelFallthroughSettings(ctx)
	return time.Duration(m.PatienceMinutes) * time.Minute, time.Duration(m.FallbackMinutes) * time.Minute
}

// handleModelFallthroughSettings serves GET/POST
// /api/settings/model-fallthrough {patienceMinutes, fallbackMinutes}.
func (s *Server) handleModelFallthroughSettings(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		m, err := s.getModelFallthroughSettings(r.Context())
		if err != nil {
			serverError(w, "reading model fallthrough settings", err)
			return
		}
		writeJSON(w, m)
	case http.MethodPost:
		var m modelFallthroughSettings
		if !readAndUnmarshal(w, r, maxRequestBody, &m) {
			return
		}
		if err := m.validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, _ := json.Marshal(m)
		if err := s.stateDB.SetSetting(r.Context(), modelFallthroughSettingKey, string(raw)); err != nil {
			serverError(w, "saving model fallthrough settings", err)
			return
		}
		writeJSON(w, m)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
