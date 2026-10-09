package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
)

func (s *Server) handleJudgeEndpoint(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	config, err := autoapprove.LoadJudgeEndpoint(r.Context(), s.stateDB)
	if err != nil {
		serverError(w, "reading reviewer endpoint", err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJudgeEndpoint(w, config)
	case http.MethodPost:
		var body struct {
			Format             string  `json:"format"`
			Endpoint           string  `json:"endpoint"`
			Model              string  `json:"model"`
			APIKey             *string `json:"apiKey"`
			MinSafeProbability float64 `json:"minSafeProbability"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		next := autoapprove.JudgeEndpoint{Format: body.Format, Endpoint: strings.TrimSpace(body.Endpoint), Model: strings.TrimSpace(body.Model), APIKey: config.APIKey, MinSafeProbability: body.MinSafeProbability}
		if next.Endpoint != config.Endpoint {
			next.APIKey = ""
		}
		if body.APIKey != nil {
			next.APIKey = *body.APIKey
		}
		if err := next.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := json.Marshal(next)
		if err != nil {
			serverError(w, "encoding reviewer endpoint", err)
			return
		}
		if err := s.stateDB.SetSetting(r.Context(), autoapprove.JudgeEndpointSettingKey, string(value)); err != nil {
			serverError(w, "saving reviewer endpoint", err)
			return
		}
		writeJudgeEndpoint(w, next)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func writeJudgeEndpoint(w http.ResponseWriter, config autoapprove.JudgeEndpoint) {
	keySet := config.APIKey != ""
	config.APIKey = ""
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, struct {
		autoapprove.JudgeEndpoint
		APIKeySet bool `json:"apiKeySet"`
	}{config, keySet})
}
