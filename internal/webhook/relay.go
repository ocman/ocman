package webhook

import (
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"net/url"
	"strings"
)

const (
	settingWebhookRelayURL        = "webhook.relay_url"
	settingWebhookEnrollmentToken = "webhook.enrollment_token"
)

type relayView struct {
	RelayURL           string `json:"relayUrl"`
	DefaultRelayURL    string `json:"defaultRelayUrl"`
	HasEnrollmentToken bool   `json:"hasEnrollmentToken"`
}

func (m *Module) relayView(r *http.Request) (relayView, error) {
	relay, _, err := m.store.GetSetting(r.Context(), settingWebhookRelayURL)
	if err != nil {
		return relayView{}, err
	}
	token, _, err := m.store.GetSetting(r.Context(), settingWebhookEnrollmentToken)
	if err != nil {
		return relayView{}, err
	}
	return relayView{RelayURL: relay, DefaultRelayURL: m.relayURL(), HasEnrollmentToken: token != ""}, nil
}

// resolveRelay picks the relay for a new inbox (saved setting, then the
// share relay) and fills an empty enrollment token from Settings. It writes the
// error response itself and reports whether registration may continue.
func (m *Module) resolveRelay(w http.ResponseWriter, r *http.Request, token *string) (string, bool) {
	relay, _, err := m.store.GetSetting(r.Context(), settingWebhookRelayURL)
	if err != nil {
		httpx.ServerError(w, "reading webhook relay", err)
		return "", false
	}
	if relay == "" {
		relay = m.relayURL()
	}
	if relay == "" {
		http.Error(w, "relay is not configured", http.StatusServiceUnavailable)
		return "", false
	}
	if *token == "" {
		if *token, _, err = m.store.GetSetting(r.Context(), settingWebhookEnrollmentToken); err != nil {
			httpx.ServerError(w, "reading webhook enrollment token", err)
			return "", false
		}
	}
	return relay, true
}

// handleRelaySetting serves /api/settings/webhook-relay. The enrollment
// token is write-only: GET reports only whether one is stored. A nil field on
// POST is left unchanged; an empty string clears it.
func (m *Module) handleRelaySetting(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var body struct {
			RelayURL        *string `json:"relayUrl"`
			EnrollmentToken *string `json:"enrollmentToken"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		if body.RelayURL != nil {
			relay := strings.TrimRight(strings.TrimSpace(*body.RelayURL), "/")
			if u, err := url.Parse(relay); relay != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
				http.Error(w, "relay URL must be an http(s) URL", http.StatusBadRequest)
				return
			}
			if err := m.store.SetSetting(r.Context(), settingWebhookRelayURL, relay); err != nil {
				httpx.ServerError(w, "saving webhook relay", err)
				return
			}
		}
		if body.EnrollmentToken != nil {
			if err := m.store.SetSetting(r.Context(), settingWebhookEnrollmentToken, strings.TrimSpace(*body.EnrollmentToken)); err != nil {
				httpx.ServerError(w, "saving webhook enrollment token", err)
				return
			}
		}
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	view, err := m.relayView(r)
	if err != nil {
		httpx.ServerError(w, "reading webhook relay settings", err)
		return
	}
	httpx.WriteJSON(w, view)
}
