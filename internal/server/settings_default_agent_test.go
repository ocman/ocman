package server

import (
	"context"
	"fmt"
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
	defaultAgentCatalog = func(_ context.Context, port, _ string) []string {
		switch port {
		case "1001":
			return []string{"build", "review"}
		case "1002":
			return []string{"review", "custom"}
		default:
			return nil
		}
	}
	if got := knownAgentOptions(context.Background(), nil); !reflect.DeepEqual(got, []string{"build", "custom", "plan", "review"}) {
		t.Fatal(got)
	}
	defaultAgentPorts = func() map[string]string { return nil }
	if got := knownAgentOptions(context.Background(), nil); !reflect.DeepEqual(got, []string{"build", "plan"}) {
		t.Fatal(got)
	}
}

func TestKnownAgentOptionsScopesDirectoriesSharingPort(t *testing.T) {
	ports, catalog, port := defaultAgentPorts, defaultAgentCatalog, defaultAgentPort
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog, defaultAgentPort = ports, catalog, port })
	defaultAgentPorts = func() map[string]string { return map[string]string{"/alpha": "1001"} }
	defaultAgentPort = func(directory string) string {
		if directory == "/offline" {
			return ""
		}
		return "1001"
	}
	defaultAgentCatalog = func(_ context.Context, port, directory string) []string {
		if port != "1001" || (directory != "/alpha" && directory != "/beta") {
			t.Fatalf("unexpected catalog target %s %s", port, directory)
		}
		return []string{strings.TrimPrefix(directory, "/") + "-agent"}
	}
	if got := knownAgentOptions(context.Background(), []string{"/alpha", "/beta", "/offline"}); !reflect.DeepEqual(got, []string{"alpha-agent", "beta-agent", "build", "plan"}) {
		t.Fatal(got)
	}
}

func TestKnownAgentOptionsStopsAfterStalledCatalog(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func() map[string]string {
		targets := make(map[string]string)
		for i := range 500 {
			targets[fmt.Sprintf("/project-%d", i)] = "1001"
		}
		return targets
	}
	calls := 0
	defaultAgentCatalog = func(ctx context.Context, _, _ string) []string {
		calls++
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := knownAgentOptions(ctx, nil); !reflect.DeepEqual(got, []string{"build", "plan"}) || calls != 1 {
		t.Fatalf("fallback = %v, catalog calls = %d, want one call", got, calls)
	}
}

func TestKnownAgentOptionsSetsOverallDeadline(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func() map[string]string { return map[string]string{"/repo": "1001"} }
	bounded := false
	defaultAgentCatalog = func(ctx context.Context, _, _ string) []string {
		deadline, ok := ctx.Deadline()
		bounded = ok && time.Until(deadline) <= 2*time.Second
		return []string{"custom"}
	}
	knownAgentOptions(context.Background(), nil)
	if !bounded {
		t.Fatal("agent discovery has no overall deadline")
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

func TestDefaultAgentOptionsIncludeLocalDatabaseDirectories(t *testing.T) {
	srv, raw := testServerWithRawDB(t)
	defer raw.Close()
	if _, err := raw.Exec(`INSERT INTO session (id, directory, time_updated) VALUES ('a', '/alpha', 1), ('b', '/beta', 1)`); err != nil {
		t.Fatal(err)
	}
	previous := defaultAgentOptions
	t.Cleanup(func() { defaultAgentOptions = previous })
	defaultAgentOptions = func(_ context.Context, directories []string) []string {
		if len(directories) != 2 || !strings.Contains(strings.Join(directories, ","), "/alpha") || !strings.Contains(strings.Join(directories, ","), "/beta") {
			t.Fatal(directories)
		}
		return []string{"custom"}
	}
	rec := httptest.NewRecorder()
	srv.handleDefaultAgent(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agents":["custom"]`) {
		t.Fatal(rec.Body)
	}
	srv.db.Close()
	defaultAgentOptions = func(_ context.Context, directories []string) []string {
		if len(directories) != 0 {
			t.Fatal(directories)
		}
		return []string{"build", "plan"}
	}
	rec = httptest.NewRecorder()
	srv.handleDefaultAgent(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"agents":["build","plan"]`) {
		t.Fatal(rec.Body)
	}
}

func TestDefaultAgentSetting(t *testing.T) {
	previous := defaultAgentOptions
	t.Cleanup(func() { defaultAgentOptions = previous })
	defaultAgentOptions = func(context.Context, []string) []string { return []string{"build", "plan", "custom-agent"} }
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
