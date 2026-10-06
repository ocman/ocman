package server

import (
	"context"
	"net/http"
	"strings"
	"unicode"
)

const defaultAgentKey = "session.default_agent"

func (s *Server) defaultAgent(ctx context.Context) (string, error) {
	value, _, err := s.stateDB.GetSetting(ctx, defaultAgentKey)
	if value == "" {
		value = "build"
	}
	return value, err
}

func (s *Server) handleDefaultAgent(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		agent, err := s.defaultAgent(r.Context())
		if err != nil {
			serverError(w, "reading default agent", err)
			return
		}
		writeJSON(w, map[string]string{"defaultAgent": agent})
	case http.MethodPost:
		var body struct {
			DefaultAgent string `json:"defaultAgent"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		body.DefaultAgent = strings.TrimSpace(body.DefaultAgent)
		if body.DefaultAgent == "" || len(body.DefaultAgent) > 128 || strings.ContainsFunc(body.DefaultAgent, unicode.IsControl) {
			http.Error(w, "defaultAgent must be a non-empty agent name of at most 128 bytes", http.StatusBadRequest)
			return
		}
		if err := s.stateDB.SetSetting(r.Context(), defaultAgentKey, body.DefaultAgent); err != nil {
			serverError(w, "saving default agent", err)
			return
		}
		writeJSON(w, body)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type settingsResponse struct {
	http.ResponseWriter
	status int
}

func (w *settingsResponse) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

// settingsHandler invalidates every client's settings cache after a successful save.
func (s *Server) settingsHandler(handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		response := &settingsResponse{ResponseWriter: w, status: http.StatusOK}
		handler(response, r)
		if r.Method == http.MethodPost && response.status < 300 {
			s.broadcastGlobalEvent("ocman.settings.changed", []byte(`{}`))
		}
	}
}
