// Package favorites owns the model favorites routes.
//
// The TUI version of OpenCode tracks favorite models in a per-user JSON
// file; ocman keeps its own equivalent in state.db so the list survives
// across browsers, works for every registered platform and does not race
// with the TUI's writer. Persistence stays in internal/state.
//
//	GET    /api/favorites?platform=<id>  → [{platform, provider, model}, ...]
//	POST   /api/favorites {platform, provider, model} → add (idempotent)
//	DELETE /api/favorites {platform, provider, model} → remove (no-op if absent)
package favorites

import (
	"context"
	"net/http"
	"strings"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
	"github.com/ocman/ocman/internal/state"
)

// Store persists favorites; *state.DB implements it.
type Store interface {
	ModelFavorites(ctx context.Context, platform string) ([]state.ModelFavorite, error)
	AddModelFavorite(ctx context.Context, platform, provider, model string) error
	RemoveModelFavorite(ctx context.Context, platform, provider, model string) error
}

type Deps struct {
	// Store is nil without a state database; the routes then answer 503.
	Store Store
	// Registered reports whether a platform ID is registered.
	Registered func(id string) bool
}

// Module is stateless; routes share the durable store.
type Module struct{ d Deps }

var _ module.Module = (*Module)(nil)

func NewModule(d Deps) *Module { return &Module{d: d} }
func (*Module) Name() string   { return "favorites" }

func (m *Module) Routes(r module.Router) {
	r.Handle("GET /api/favorites", m.handleList)
	r.Handle("POST /api/favorites", m.handleAdd)
	r.Handle("DELETE /api/favorites", m.handleRemove)
}

// entry is the wire shape for one favorited model. Kept minimal so the
// frontend can round-trip it unchanged.
type entry struct {
	Platform string `json:"platform"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleList returns every favorite for the given platform. The ?platform=
// query is required: favorites never leak across platforms, and there is
// no obvious default when several are registered.
func (m *Module) handleList(w http.ResponseWriter, r *http.Request) {
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	if !m.available(w) || !m.validPlatform(w, platform) {
		return
	}
	rows, err := m.d.Store.ModelFavorites(r.Context(), platform)
	if err != nil {
		httpx.ServerError(w, "listing model favorites", err)
		return
	}
	out := make([]entry, 0, len(rows))
	for _, f := range rows {
		out = append(out, entry{Platform: f.Platform, Provider: f.Provider, Model: f.Model})
	}
	httpx.WriteJSON(w, out)
}

// handleAdd marks a (platform, provider, model) triple as a favorite.
// Idempotent: repeated calls succeed.
func (m *Module) handleAdd(w http.ResponseWriter, r *http.Request) {
	req, ok := m.readBody(w, r)
	if !ok {
		return
	}
	if err := m.d.Store.AddModelFavorite(r.Context(), req.Platform, req.Provider, req.Model); err != nil {
		httpx.ServerError(w, "adding model favorite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemove deletes a favorite. No error if it wasn't set.
func (m *Module) handleRemove(w http.ResponseWriter, r *http.Request) {
	req, ok := m.readBody(w, r)
	if !ok {
		return
	}
	if err := m.d.Store.RemoveModelFavorite(r.Context(), req.Platform, req.Provider, req.Model); err != nil {
		httpx.ServerError(w, "removing model favorite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readBody decodes and validates the POST/DELETE body shared between add
// and remove.
func (m *Module) readBody(w http.ResponseWriter, r *http.Request) (entry, bool) {
	if !m.available(w) {
		return entry{}, false
	}
	var req entry
	if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &req) {
		return entry{}, false
	}
	req.Platform = strings.TrimSpace(req.Platform)
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if !m.validPlatform(w, req.Platform) {
		return entry{}, false
	}
	if req.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return entry{}, false
	}
	// Provider may legitimately be empty for platforms without a provider
	// concept.
	return req, true
}

func (m *Module) available(w http.ResponseWriter) bool {
	if m.d.Store == nil {
		http.Error(w, "favorites unavailable", http.StatusServiceUnavailable)
		return false
	}
	return true
}

// validPlatform rejects empty and unregistered platform IDs, writing the
// error response.
func (m *Module) validPlatform(w http.ResponseWriter, platform string) bool {
	if platform == "" {
		http.Error(w, "platform is required", http.StatusBadRequest)
		return false
	}
	if m.d.Registered == nil || !m.d.Registered(platform) {
		http.Error(w, "unknown platform", http.StatusBadRequest)
		return false
	}
	return true
}
