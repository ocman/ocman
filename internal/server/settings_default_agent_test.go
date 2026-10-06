package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProjectSettingsDefaultAgent(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.getProjectSettings(rec, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/repo", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"defaultAgent":"build"`) {
		t.Fatalf("default agent: %d %s", rec.Code, rec.Body)
	}
}

func TestDefaultAgentSetting(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	for _, tc := range []struct {
		method, body, want string
		status             int
	}{
		{"GET", "", `"defaultAgent":"build"`, 200},
		{"POST", `{"defaultAgent":" plan "}`, `"defaultAgent":"plan"`, 200},
		{"GET", "", `"defaultAgent":"plan"`, 200},
		{"POST", `{"defaultAgent":"custom-agent"}`, `"defaultAgent":"custom-agent"`, 200},
		{"POST", `{"defaultAgent":" "}`, "", 400},
		{"POST", `{"defaultAgent":"bad\nname"}`, "", 400},
		{"POST", `{"defaultAgent":"` + strings.Repeat("x", 129) + `"}`, "", 400},
		{"POST", `{`, "", 400},
		{"DELETE", "", "", 405},
	} {
		rec := httptest.NewRecorder()
		srv.handleDefaultAgent(rec, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.want) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.body, rec.Code, rec.Body)
		}
	}
	rec := httptest.NewRecorder()
	srv.getProjectSettings(rec, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/other", nil))
	if !strings.Contains(rec.Body.String(), `"defaultAgent":"custom-agent"`) {
		t.Fatal(rec.Body)
	}
	rec = httptest.NewRecorder()
	(&Server{}).handleDefaultAgent(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 503 {
		t.Fatal(rec.Code)
	}
}

func TestSettingsSaveInvalidation(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t), broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	for _, tc := range []struct {
		method, body string
		event        bool
	}{
		{"GET", "", false},
		{"POST", `{"defaultAgent":"plan"}`, true},
		{"POST", `{"defaultAgent":""}`, false},
	} {
		rec := httptest.NewRecorder()
		srv.settingsHandler(srv.handleDefaultAgent)(rec, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
		select {
		case event := <-sub.ch:
			if !tc.event || event.event != "ocman.settings.changed" {
				t.Fatalf("unexpected event: %+v", event)
			}
		case <-time.After(20 * time.Millisecond):
			if tc.event {
				t.Fatal("missing invalidation event")
			}
		}
	}
}
