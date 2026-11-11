package webhook

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/httpx"
	"github.com/ocman/ocman/internal/module"
)

type relayConnection struct {
	mu               sync.Mutex
	relay, pollToken string
	expiresAt        int64
}

func (m *Module) connectionRoutes(r module.Router) {
	r.Local("POST /api/settings/webhook-relay/connect", m.handleConnect)
	r.Local("POST /api/settings/webhook-relay/connect/poll", m.handleConnectPoll)
	r.Local("DELETE /api/settings/webhook-relay/connect", m.handleConnectCancel)
}

func (m *Module) effectiveRelay(r *http.Request) (string, error) {
	v, err := m.relayView(r)
	if err != nil {
		return "", err
	}
	if v.RelayURL != "" {
		return v.RelayURL, nil
	}
	if v.DefaultRelayURL == "" {
		return "", errors.New("configure a relay URL first")
	}
	return v.DefaultRelayURL, nil
}

func relayAuthRequest(r *http.Request, relay, path, token string, target any) (int, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, strings.TrimRight(relay, "/")+path, nil)
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		return 0, errors.New("could not reach relay")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusAccepted {
		return response.StatusCode, errors.New("relay sign-in unavailable or connection expired; start a new connection or use manual enrollment")
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(target); err != nil {
		return 0, errors.New("invalid relay sign-in response")
	}
	return response.StatusCode, nil
}

func (m *Module) handleConnect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m.connection.mu.Lock()
	defer m.connection.mu.Unlock()
	relay, err := m.effectiveRelay(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var result struct {
		AuthorizationURL string `json:"authorizationUrl"`
		PollToken        string `json:"pollToken"`
		ExpiresAt        int64  `json:"expiresAt"`
	}
	if _, err := relayAuthRequest(r, relay, "/auth/connect", "", &result); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	u, err := url.Parse(result.AuthorizationURL)
	base, baseErr := url.Parse(relay)
	if err != nil || baseErr != nil || u.Scheme != base.Scheme || u.Host != base.Host || u.User != nil || !strings.HasPrefix(u.Path, "/auth/connect/") || result.PollToken == "" || result.ExpiresAt <= time.Now().UnixMilli() {
		http.Error(w, "invalid relay connection", http.StatusBadGateway)
		return
	}
	m.connection.relay, m.connection.pollToken, m.connection.expiresAt = relay, result.PollToken, min(result.ExpiresAt, time.Now().Add(10*time.Minute).UnixMilli())
	httpx.WriteJSON(w, map[string]any{"authorizationUrl": result.AuthorizationURL, "expiresAt": m.connection.expiresAt})
}

func (m *Module) handleConnectPoll(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	m.connection.mu.Lock()
	defer m.connection.mu.Unlock()
	relay, err := m.effectiveRelay(r)
	if err != nil || relay != m.connection.relay || m.connection.pollToken == "" || time.Now().UnixMilli() >= m.connection.expiresAt {
		m.connection.pollToken = ""
		http.Error(w, "connection expired or relay changed; connect again", http.StatusConflict)
		return
	}
	var result struct {
		EnrollmentToken string `json:"enrollmentToken"`
	}
	status, err := relayAuthRequest(r, relay, "/auth/poll", m.connection.pollToken, &result)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if status == http.StatusAccepted {
		httpx.WriteJSON(w, map[string]bool{"pending": true})
		return
	}
	if result.EnrollmentToken == "" {
		http.Error(w, "relay returned no enrollment token", http.StatusBadGateway)
		return
	}
	if err := m.store.SetSettings(r.Context(), map[string]string{settingWebhookEnrollmentToken: result.EnrollmentToken, settingWebhookEnrollmentRelay: relay}); err != nil {
		http.Error(w, "could not save enrollment token; check configuration and connect again", http.StatusConflict)
		return
	}
	m.connection.pollToken = ""
	v, err := m.relayView(r)
	if err != nil {
		httpx.ServerError(w, "reading connected relay", err)
		return
	}
	httpx.WriteJSON(w, map[string]any{"pending": false, "settings": v})
}

func (m *Module) handleConnectCancel(w http.ResponseWriter, _ *http.Request) {
	m.connection.mu.Lock()
	m.connection.pollToken = ""
	m.connection.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
