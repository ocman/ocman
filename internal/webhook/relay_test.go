package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWebhookFileConfigurationRefusesExternalTokenUpdates(t *testing.T) {
	m, handler := testModule(t)
	t.Setenv("WEBHOOK_CONFIG_TEST", "external-enrollment-secret")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("version: 1\nsettings:\n  webhook.enrollment_token: secret://env/WEBHOOK_CONFIG_TEST\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.store.EnableFileConfig(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"enrollmentToken":"replacement"}`, `{"enrollmentToken":""}`} {
		rr := do(t, handler, http.MethodPost, "/api/settings/webhook-relay", body)
		if rr.Code != http.StatusConflict || strings.Contains(rr.Body.String(), "external-enrollment-secret") {
			t.Fatalf("external token edit = %d %s", rr.Code, rr.Body.String())
		}
	}
	before, _ := os.ReadFile(path)
	rr := do(t, handler, http.MethodPost, "/api/settings/webhook-relay", `{"relayUrl":"https://new-relay.example","enrollmentToken":"replacement"}`)
	after, _ := os.ReadFile(path)
	if rr.Code != http.StatusConflict || string(before) != string(after) {
		t.Fatal("refused secret update partially saved the relay URL")
	}
}

func TestWebhookRelaySettingFeedsRegistration(t *testing.T) {
	var gotAuth string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"inbox","ingestionUrl":"/i/inbox","managementToken":"m","fetchToken":"f","acknowledgmentToken":"a","keyVersion":1}`))
	}))
	defer relay.Close()

	m, handler := testModule(t)
	relayURL = "https://share.default"
	const path = "/api/settings/webhook-relay"

	var view relayView
	rec := do(t, handler, http.MethodGet, path, "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != "" || view.DefaultRelayURL != "https://share.default" || view.HasEnrollmentToken {
		t.Fatalf("initial: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{`{"relayUrl":"ftp://x"}`, `{"relayUrl":"not a url"}`, `{"`} {
		if rec := do(t, handler, http.MethodPost, path, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	if rec := do(t, handler, http.MethodDelete, path, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = do(t, handler, http.MethodPost, path, `{"relayUrl":" `+relay.URL+`/ ","enrollmentToken":" enroll "}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "enroll") || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != relay.URL || !view.HasEnrollmentToken {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// Updating only the URL keeps the stored token.
	if rec := do(t, handler, http.MethodPost, path, `{"relayUrl":"`+relay.URL+`"}`); !strings.Contains(rec.Body.String(), `"hasEnrollmentToken":true`) {
		t.Fatalf("partial save: %s", rec.Body.String())
	}

	rec = do(t, handler, http.MethodPost, "/api/webhook-inboxes", `{"name":"forgejo"}`)
	if rec.Code != http.StatusCreated || gotAuth != "Bearer enroll" || !strings.Contains(rec.Body.String(), `"relayUrl":"`+relay.URL+`"`) {
		t.Fatalf("register: %d %s auth=%q", rec.Code, rec.Body.String(), gotAuth)
	}

	// Clearing both falls back to the share relay and sends no stored token.
	rec = do(t, handler, http.MethodPost, path, `{"relayUrl":"","enrollmentToken":""}`)
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != "" || view.HasEnrollmentToken {
		t.Fatalf("clear: %s", rec.Body.String())
	}
	token := ""
	if got, ok := m.resolveRelay(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), &token); !ok || got != "https://share.default" || token != "" {
		t.Fatalf("fallback = %q %v %q", got, ok, token)
	}
	relayURL = ""
	w := httptest.NewRecorder()
	if _, ok := m.resolveRelay(w, httptest.NewRequest(http.MethodPost, "/", nil), &token); ok || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no relay: %v %d", ok, w.Code)
	}
}
