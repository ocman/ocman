package projectsmod

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
)

// handleSettings serves a project's ordered model list, keyed by
// the folded project root (a worktree shares its repository's entry).
// Local-only: the list lives in the hub's state.db.
//
// GET  /api/project/settings?dir=<abs>
// POST /api/project/settings  { directory, models, off }
func (m *Module) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		m.getSettings(w, r)
	case http.MethodPost:
		m.postSettings(w, r)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (m *Module) getSettings(w http.ResponseWriter, r *http.Request) {
	dir := strings.TrimSpace(r.URL.Query().Get("dir"))
	if dir == "" {
		http.Error(w, "dir is required", http.StatusBadRequest)
		return
	}
	ps, err := m.d.Store.GetProjectSettings(r.Context(), dir)
	if err != nil {
		httpx.ServerError(w, "reading project settings", err)
		return
	}
	agent, err := m.d.DefaultAgent(r.Context())
	if err != nil {
		httpx.ServerError(w, "reading default agent", err)
		return
	}
	defaults, err := LoadDefaults(r.Context(), m.d.Store, dir, r.URL.Query().Get("remoteId"))
	if err != nil {
		httpx.ServerError(w, "reading project defaults", err)
		return
	}
	if defaults != nil && defaults.Agent != "" {
		agent = defaults.Agent
	}
	httpx.WriteJSON(w, struct {
		state.ProjectSettings
		DefaultAgent string    `json:"defaultAgent"`
		Defaults     *Defaults `json:"defaults,omitempty"`
	}{ps, agent, defaults})
}

func (m *Module) postSettings(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Directory string    `json:"directory"`
		RemoteID  string    `json:"remoteId"`
		Defaults  *Defaults `json:"defaults"`
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
		if err := m.d.Store.SetSetting(r.Context(), DefaultsKey(dir, req.RemoteID), string(raw)); err != nil {
			httpx.ServerError(w, "saving project defaults", err)
			return
		}
		httpx.WriteJSON(w, map[string]bool{"ok": true})
		return
	}
	if err := m.d.Store.SetProjectSettings(r.Context(), dir, req.ProjectSettings); err != nil {
		if errors.Is(err, state.ErrInvalidProjectSettings) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		httpx.ServerError(w, "saving project settings", err)
		return
	}
	m.d.ForgetProjectModels(dir)
	httpx.WriteJSON(w, map[string]bool{"ok": true})
}
