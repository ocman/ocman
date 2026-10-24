package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func TestProjectSettingsDefaultAgent(t *testing.T) {
	srv := &Server{stateDB: openTestStateDB(t)}
	rec := httptest.NewRecorder()
	srv.getProjectSettings(rec, httptest.NewRequest(http.MethodGet, "/api/project/settings?dir=/repo", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"defaultAgent":"build"`) {
		t.Fatalf("default agent: %d %s", rec.Code, rec.Body)
	}
}

func TestDefaultAgentOptionsIncludeStoredDirectories(t *testing.T) {
	srv, _, _ := identityHTTPFixture(t)
	history := srv.historyAnalytics.Store
	if err := history.RegisterInstance(t.Context(), statev2.Instance{ID: "options", OwnerID: "owner", Platform: "test", SourceKey: "options"}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/alpha", "/beta"} {
		if _, err := history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "options", SessionID: dir}, Directory: dir}}); err != nil {
			t.Fatal(err)
		}
	}
	previous := defaultAgentOptions
	t.Cleanup(func() { defaultAgentOptions = previous })
	defaultAgentOptions = func(_ context.Context, _ []platforms.Platform, dirs []string) []string {
		if strings.Join(dirs, ",") != "/alpha,/beta" {
			t.Fatal(dirs)
		}
		return []string{"custom"}
	}
	rec := httptest.NewRecorder()
	serveSetting(srv.defaultAgentSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agents":["custom"]`) {
		t.Fatal(rec.Body)
	}
	if err := history.Close(); err != nil {
		t.Fatal(err)
	}
	defaultAgentOptions = func(_ context.Context, _ []platforms.Platform, dirs []string) []string {
		if len(dirs) != 0 {
			t.Fatal(dirs)
		}
		return []string{"build", "plan"}
	}
	rec = httptest.NewRecorder()
	serveSetting(srv.defaultAgentSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agents":["build","plan"]`) {
		t.Fatal(rec.Body)
	}
}

func TestDefaultAgentSetting(t *testing.T) {
	previous := defaultAgentOptions
	t.Cleanup(func() { defaultAgentOptions = previous })
	defaultAgentOptions = func(context.Context, []platforms.Platform, []string) []string {
		return []string{"build", "plan", "custom-agent"}
	}
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
		{"POST", `{`, "", 400}, {"DELETE", "", "", 405},
	} {
		rec := httptest.NewRecorder()
		serveSetting(srv.defaultAgentSetting()).ServeHTTP(rec, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
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
	serveSetting((&Server{}).defaultAgentSetting()).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
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
	}{{"GET", "", false}, {"POST", `{"defaultAgent":"plan"}`, true}, {"POST", `{"defaultAgent":""}`, false}} {
		rec := httptest.NewRecorder()
		srv.settingsHandler(serveSetting(srv.defaultAgentSetting()).ServeHTTP)(rec, httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body)))
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
