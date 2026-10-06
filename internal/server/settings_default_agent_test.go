package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestKnownAgentOptions(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func() map[string]string { return map[string]string{"/a": "1001", "/b": "1002", "/c": "1003"} }
	defaultAgentCatalog = func(_ context.Context, endpoint, directory string) ([]string, []string, error) {
		if directory != "" {
			t.Fatal(directory)
		}
		switch endpoint {
		case "http://127.0.0.1:1001":
			return []string{"build", "review"}, nil, nil
		case "http://127.0.0.1:1002":
			return []string{"review", "custom"}, nil, nil
		default:
			return nil, nil, errors.New("offline")
		}
	}
	if got := knownAgentOptions(context.Background()); !reflect.DeepEqual(got, []string{"build", "custom", "plan", "review"}) {
		t.Fatal(got)
	}
	defaultAgentPorts = func() map[string]string { return nil }
	if got := knownAgentOptions(context.Background()); !reflect.DeepEqual(got, []string{"build", "plan"}) {
		t.Fatal(got)
	}
}

func TestProjectSettingsDefaultAgent(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.getProjectSettings(rec, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/repo", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"defaultAgent":"build"`) {
		t.Fatalf("default agent: %d %s", rec.Code, rec.Body)
	}
}

func TestDefaultAgentSetting(t *testing.T) {
	previous := defaultAgentOptions
	t.Cleanup(func() { defaultAgentOptions = previous })
	defaultAgentOptions = func(context.Context) []string { return []string{"build", "plan", "custom-agent"} }
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
		if tc.method == "GET" && !strings.Contains(rec.Body.String(), `"agents":["build","plan","custom-agent"]`) {
			t.Fatal(rec.Body)
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
