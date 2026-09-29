package server

import (
	"net/http"
	"net/url"
	"strings"
)

const (
	settingWebhookRelayURL        = "webhook.relay_url"
	settingWebhookEnrollmentToken = "webhook.enrollment_token"
)

type webhookRelayView struct {
	RelayURL           string `json:"relayUrl"`
	DefaultRelayURL    string `json:"defaultRelayUrl"`
	HasEnrollmentToken bool   `json:"hasEnrollmentToken"`
}

func (s *Server) webhookRelayView(r *http.Request) (webhookRelayView, error) {
	relay, _, err := s.stateDB.GetSetting(r.Context(), settingWebhookRelayURL)
	if err != nil {
		return webhookRelayView{}, err
	}
	token, _, err := s.stateDB.GetSetting(r.Context(), settingWebhookEnrollmentToken)
	if err != nil {
		return webhookRelayView{}, err
	}
	return webhookRelayView{RelayURL: relay, DefaultRelayURL: s.relayURL, HasEnrollmentToken: token != ""}, nil
}

// resolveWebhookRelay picks the relay for a new inbox (saved setting, then the
// share relay) and fills an empty enrollment token from Settings. It writes the
// error response itself and reports whether registration may continue.
func (s *Server) resolveWebhookRelay(w http.ResponseWriter, r *http.Request, token *string) (string, bool) {
	relay, _, err := s.stateDB.GetSetting(r.Context(), settingWebhookRelayURL)
	if err != nil {
		serverError(w, "reading webhook relay", err)
		return "", false
	}
	if relay == "" {
		relay = s.relayURL
	}
	if relay == "" {
		http.Error(w, "relay is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	if *token == "" {
		if *token, _, err = s.stateDB.GetSetting(r.Context(), settingWebhookEnrollmentToken); err != nil {
			serverError(w, "reading webhook enrollment token", err)
			return "", false
		}
	}
	return relay, true
}

// handleWebhookRelaySetting serves /api/settings/webhook-relay. The enrollment
// token is write-only: GET reports only whether one is stored. A nil field on
// POST is left unchanged; an empty string clears it.
func (s *Server) handleWebhookRelaySetting(w http.ResponseWriter, r *http.Request) {
	if s.stateDB == nil {
		http.Error(w, "state database not available", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var body struct {
			RelayURL        *string `json:"relayUrl"`
			EnrollmentToken *string `json:"enrollmentToken"`
		}
		if !readAndUnmarshal(w, r, maxRequestBody, &body) {
			return
		}
		if body.RelayURL != nil {
			relay := strings.TrimRight(strings.TrimSpace(*body.RelayURL), "/")
			if u, err := url.Parse(relay); relay != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
				http.Error(w, "relay URL must be an http(s) URL", http.StatusBadRequest)
				return
			}
			if err := s.stateDB.SetSetting(r.Context(), settingWebhookRelayURL, relay); err != nil {
				serverError(w, "saving webhook relay", err)
				return
			}
		}
		if body.EnrollmentToken != nil {
			if err := s.stateDB.SetSetting(r.Context(), settingWebhookEnrollmentToken, strings.TrimSpace(*body.EnrollmentToken)); err != nil {
				serverError(w, "saving webhook enrollment token", err)
				return
			}
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := s.webhookRelayView(r)
	if err != nil {
		serverError(w, "reading webhook relay settings", err)
		return
	}
	writeJSON(w, view)
}
