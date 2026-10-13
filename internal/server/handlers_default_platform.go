package server

import (
	"net/http"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// handleDefaultPlatform reads or saves the platform a new conversation
// starts on when the composer has not picked one (FR-2).
func (s *Server) handleDefaultPlatform(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Platform string `json:"platform"`
	}
	switch r.Method {
	case http.MethodGet:
		platform, err := s.stateDB.DefaultPlatform(r.Context())
		if err != nil {
			serverError(w, "reading default platform", err)
			return
		}
		body.Platform = platform
	case http.MethodPost:
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		if _, ok := s.registry.Get(platforms.ID(body.Platform)); !ok || isRemotePlatformID(body.Platform) {
			http.Error(w, "unknown platform", http.StatusBadRequest)
			return
		}
		if err := s.stateDB.SetDefaultPlatform(r.Context(), body.Platform); err != nil {
			serverError(w, "saving default platform", err)
			return
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, body)
}
