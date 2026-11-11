package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayConnectKeepsCredentialsServerSide(t *testing.T) {
	var base string
	ready := false
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/connect":
			_ = json.NewEncoder(w).Encode(map[string]any{"authorizationUrl": base + "/auth/connect/abc", "pollToken": "private-poll", "expiresAt": int64(9999999999999)})
		case "/auth/poll":
			if r.Header.Get("Authorization") != "Bearer private-poll" {
				t.Error("missing server-side poll token")
			}
			if !ready {
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(`{"status":"pending"}`))
				return
			}
			_, _ = w.Write([]byte(`{"enrollmentToken":"private-enrollment"}`))
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer relay.Close()
	base = relay.URL
	m, handler := testModule(t)
	relayURL = base
	rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/auth/connect/abc") || strings.Contains(rec.Body.String(), "private-") {
		t.Fatalf("connect: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect/poll", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pending":true`) {
		t.Fatalf("pending: %d %s", rec.Code, rec.Body)
	}
	ready = true
	rec = do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect/poll", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "private-") || !strings.Contains(rec.Body.String(), `"hasEnrollmentToken":true`) {
		t.Fatalf("complete: %d %s", rec.Code, rec.Body)
	}
	token, _, _ := m.store.GetSetting(t.Context(), settingWebhookEnrollmentToken)
	if token != "private-enrollment" {
		t.Fatal("enrollment was not saved")
	}
}

func TestChangingRelayClearsEnrollment(t *testing.T) {
	_, handler := testModule(t)
	do(t, handler, http.MethodPost, "/api/settings/webhook-relay", `{"relayUrl":"https://first.example","enrollmentToken":"private"}`)
	rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay", `{"relayUrl":"https://second.example"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"hasEnrollmentToken":false`) {
		t.Fatalf("credential crossed relay boundary: %d %s", rec.Code, rec.Body)
	}
}

func TestConnectRejectsInvalidRelayResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unavailable", `provider-private-error`, http.StatusNotFound},
		{"malformed", `invalid`, http.StatusOK},
		{"foreign URL", `{"authorizationUrl":"https://other.example/auth/connect/id","pollToken":"private","expiresAt":9999999999999}`, http.StatusCreated},
		{"missing token", `{"authorizationUrl":"BASE/auth/connect/id","expiresAt":9999999999999}`, http.StatusCreated},
		{"expired", `{"authorizationUrl":"BASE/auth/connect/id","pollToken":"private","expiresAt":1}`, http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var base string
			relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(strings.ReplaceAll(tc.body, "BASE", base)))
			}))
			defer relay.Close()
			base = relay.URL
			_, handler := testModule(t)
			relayURL = base
			rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect", "")
			if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "private") {
				t.Fatalf("invalid relay response: %d %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestConnectCancellationExpiryAndRelayBinding(t *testing.T) {
	m, handler := testModule(t)
	if rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect", ""); rec.Code != http.StatusBadRequest {
		t.Fatal("connection without relay accepted")
	}
	relayURL = "https://relay.example"
	m.connection.relay, m.connection.pollToken, m.connection.expiresAt = relayURL, "private", time.Now().Add(time.Minute).UnixMilli()
	if rec := do(t, handler, http.MethodDelete, "/api/settings/webhook-relay/connect", ""); rec.Code != http.StatusNoContent || m.connection.pollToken != "" {
		t.Fatal("cancel failed")
	}
	if rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect/poll", ""); rec.Code != http.StatusConflict {
		t.Fatal("canceled poll accepted")
	}
	m.connection.pollToken, m.connection.expiresAt = "private", 1
	if rec := do(t, handler, http.MethodPost, "/api/settings/webhook-relay/connect/poll", ""); rec.Code != http.StatusConflict {
		t.Fatal("expired poll accepted")
	}
	if err := m.store.SetSettings(t.Context(), map[string]string{settingWebhookEnrollmentToken: "private", settingWebhookEnrollmentRelay: relayURL}); err != nil {
		t.Fatal(err)
	}
	relayURL = "https://changed.example"
	rec := do(t, handler, http.MethodGet, "/api/settings/webhook-relay", "")
	if !strings.Contains(rec.Body.String(), `"hasEnrollmentToken":false`) {
		t.Fatalf("stale connection shown as active: %s", rec.Body)
	}
	token := ""
	w := httptest.NewRecorder()
	if _, ok := m.resolveRelay(w, httptest.NewRequest(http.MethodPost, "/", nil), &token); ok || w.Code != http.StatusConflict || token != "" {
		t.Fatal("SSO credential crossed default relay boundary")
	}
}
