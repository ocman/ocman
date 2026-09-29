package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWebhookRelaySettingFeedsRegistration(t *testing.T) {
	var gotAuth string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"inbox","ingestionUrl":"/i/inbox","managementToken":"m","fetchToken":"f","acknowledgmentToken":"a","keyVersion":1}`))
	}))
	defer relay.Close()

	srv, handler, _ := routineHTTPServer(t)
	srv.relayURL = "https://share.default"
	const path = "/api/settings/webhook-relay"

	var view webhookRelayView
	rec := doRoutineRequest(t, handler, http.MethodGet, path, "")
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != "" || view.DefaultRelayURL != "https://share.default" || view.HasEnrollmentToken {
		t.Fatalf("initial: %d %s", rec.Code, rec.Body.String())
	}
	for _, bad := range []string{`{"relayUrl":"ftp://x"}`, `{"relayUrl":"not a url"}`, `{"`} {
		if rec := doRoutineRequest(t, handler, http.MethodPost, path, bad); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	if rec := doRoutineRequest(t, handler, http.MethodDelete, path, ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("delete: %d", rec.Code)
	}
	rec = doRoutineRequest(t, handler, http.MethodPost, path, `{"relayUrl":" `+relay.URL+`/ ","enrollmentToken":" enroll "}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "enroll") || json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != relay.URL || !view.HasEnrollmentToken {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	// Updating only the URL keeps the stored token.
	if rec := doRoutineRequest(t, handler, http.MethodPost, path, `{"relayUrl":"`+relay.URL+`"}`); !strings.Contains(rec.Body.String(), `"hasEnrollmentToken":true`) {
		t.Fatalf("partial save: %s", rec.Body.String())
	}

	rec = doRoutineRequest(t, handler, http.MethodPost, "/api/webhook-inboxes", `{"name":"forgejo"}`)
	if rec.Code != http.StatusCreated || gotAuth != "Bearer enroll" || !strings.Contains(rec.Body.String(), `"relayUrl":"`+relay.URL+`"`) {
		t.Fatalf("register: %d %s auth=%q", rec.Code, rec.Body.String(), gotAuth)
	}

	// Clearing both falls back to the share relay and sends no stored token.
	rec = doRoutineRequest(t, handler, http.MethodPost, path, `{"relayUrl":"","enrollmentToken":""}`)
	if json.Unmarshal(rec.Body.Bytes(), &view) != nil || view.RelayURL != "" || view.HasEnrollmentToken {
		t.Fatalf("clear: %s", rec.Body.String())
	}
	token := ""
	if got, ok := srv.resolveWebhookRelay(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/", nil), &token); !ok || got != "https://share.default" || token != "" {
		t.Fatalf("fallback = %q %v %q", got, ok, token)
	}
	srv.relayURL = ""
	w := httptest.NewRecorder()
	if _, ok := srv.resolveWebhookRelay(w, httptest.NewRequest(http.MethodPost, "/", nil), &token); ok || w.Code != http.StatusServiceUnavailable {
		t.Fatalf("no relay: %v %d", ok, w.Code)
	}
}
