package server

import (
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// --- Model favorites ---
//
// The TUI version of OpenCode tracks favorite models in a per-user
// JSON file; ocman maintains its own equivalent in state.db so the
// list survives across browsers, works for every registered
// platform, and doesn't race with the TUI's writer. See
// internal/state/migrate.go v4 for the schema.
//
// Route layout:
//
//	GET    /api/favorites?platform=<id>  → [{provider, model}, ...]
//	POST   /api/favorites {platform, provider, model} → add (idempotent)
//	DELETE /api/favorites {platform, provider, model} → remove (no-op if absent)

// favoriteEntry is the wire shape for one favorited model. Kept
// minimal so the frontend can round-trip it unchanged.
type favoriteEntry struct {
	Platform string `json:"platform"`
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// handleFavoritesRoot routes /api/favorites by method.
func (s *Server) handleFavoritesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListFavorites(w, r)
	case http.MethodPost:
		s.handleAddFavorite(w, r)
	case http.MethodDelete:
		s.handleRemoveFavorite(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleListFavorites returns every favorite for the given platform.
// The ?platform= query is required: we don't leak favorites across
// platforms, and there's no obvious "default" when multiple are
// registered.
func (s *Server) handleListFavorites(w http.ResponseWriter, r *http.Request) {
	platform := strings.TrimSpace(r.URL.Query().Get("platform"))
	if !s.validFavoritesPlatform(w, platform) {
		return
	}

	rows, err := s.stateDB.ModelFavorites(r.Context(), platform)
	if err != nil {
		serverError(w, "listing model favorites", err)
		return
	}

	out := make([]favoriteEntry, 0, len(rows))
	for _, f := range rows {
		out = append(out, favoriteEntry{Platform: f.Platform, Provider: f.Provider, Model: f.Model})
	}
	writeJSON(w, out)
}

// handleAddFavorite marks a (platform, provider, model) triple as a
// favorite. Idempotent: repeated calls succeed.
func (s *Server) handleAddFavorite(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readFavoriteBody(w, r)
	if !ok {
		return
	}
	if err := s.stateDB.AddModelFavorite(r.Context(), req.Platform, req.Provider, req.Model); err != nil {
		serverError(w, "adding model favorite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRemoveFavorite deletes a favorite. No error if it wasn't set.
func (s *Server) handleRemoveFavorite(w http.ResponseWriter, r *http.Request) {
	req, ok := s.readFavoriteBody(w, r)
	if !ok {
		return
	}
	if err := s.stateDB.RemoveModelFavorite(r.Context(), req.Platform, req.Provider, req.Model); err != nil {
		serverError(w, "removing model favorite", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readFavoriteBody decodes + validates the POST/DELETE body shared
// between add and remove.
func (s *Server) readFavoriteBody(w http.ResponseWriter, r *http.Request) (favoriteEntry, bool) {
	var req favoriteEntry
	if !readAndUnmarshal(w, r, maxRequestBody, &req) {
		return favoriteEntry{}, false
	}
	req.Platform = strings.TrimSpace(req.Platform)
	req.Provider = strings.TrimSpace(req.Provider)
	req.Model = strings.TrimSpace(req.Model)
	if !s.validFavoritesPlatform(w, req.Platform) {
		return favoriteEntry{}, false
	}
	if req.Model == "" {
		http.Error(w, "model is required", http.StatusBadRequest)
		return favoriteEntry{}, false
	}
	// Provider may legitimately be empty for platforms that don't
	// have a provider concept. No validation beyond
	// trimming.
	return req, true
}

// validFavoritesPlatform rejects empty / unregistered platform ids.
// Returning false means an error response has already been written.
func (s *Server) validFavoritesPlatform(w http.ResponseWriter, platform string) bool {
	if platform == "" {
		http.Error(w, "platform is required", http.StatusBadRequest)
		return false
	}
	if _, ok := s.registry.Get(platforms.ID(platform)); !ok {
		http.Error(w, "unknown platform", http.StatusBadRequest)
		return false
	}
	return true
}
