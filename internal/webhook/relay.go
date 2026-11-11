package webhook

import (
	"errors"
	"net/http"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/state"
	"net/url"
	"strings"
)

const (
	settingWebhookRelayURL        = "webhook.relay_url"
	settingWebhookEnrollmentToken = "webhook.enrollment_token"
	settingWebhookEnrollmentRelay = "webhook.enrollment_relay_url"
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
	boundRelay, _, err := m.store.GetSetting(r.Context(), settingWebhookEnrollmentRelay)
	if err != nil {
		return relayView{}, err
	}
	effective := relay
	if effective == "" {
		effective = m.relayURL()
	}
	return relayView{RelayURL: relay, DefaultRelayURL: m.relayURL(), HasEnrollmentToken: token != "" && (boundRelay == "" || boundRelay == effective)}, nil
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
		boundRelay, _, err := m.store.GetSetting(r.Context(), settingWebhookEnrollmentRelay)
		if err != nil || (boundRelay != "" && boundRelay != relay) {
			http.Error(w, "relay changed; connect again", http.StatusConflict)
			return "", false
		}
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
		m.connection.mu.Lock()
		defer m.connection.mu.Unlock()
		var body struct {
			RelayURL        *string `json:"relayUrl"`
			EnrollmentToken *string `json:"enrollmentToken"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		updates := map[string]string{}
		if body.RelayURL != nil {
			relay := strings.TrimRight(strings.TrimSpace(*body.RelayURL), "/")
			if u, err := url.Parse(relay); relay != "" && (err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "") {
				http.Error(w, "relay URL must be an http(s) URL", http.StatusBadRequest)
				return
			}
			updates[settingWebhookRelayURL] = relay
			previous, _, err := m.store.GetSetting(r.Context(), settingWebhookRelayURL)
			if err != nil {
				httpx.ServerError(w, "reading webhook relay", err)
				return
			}
			if previous != relay {
				updates[settingWebhookEnrollmentToken] = ""
				m.connection.pollToken = ""
			}
		}
		if body.EnrollmentToken != nil {
			updates[settingWebhookEnrollmentToken] = strings.TrimSpace(*body.EnrollmentToken)
			updates[settingWebhookEnrollmentRelay] = ""
		}
		if len(updates) > 0 {
			if err := m.store.SetSettings(r.Context(), updates); err != nil {
				if errors.Is(err, state.ErrExternalSecret) {
					http.Error(w, err.Error(), http.StatusConflict)
					return
				}
				httpx.ServerError(w, "saving webhook relay settings", err)
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
