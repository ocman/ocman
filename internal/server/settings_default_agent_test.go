package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKnownAgentOptions(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func(context.Context) map[string]string {
		return map[string]string{"/a": "1001", "/b": "1002", "/c": "1003"}
	}
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
	defaultAgentPorts = func(context.Context) map[string]string { return nil }
	if got := knownAgentOptions(context.Background(), nil); !reflect.DeepEqual(got, []string{"build", "plan"}) {
		t.Fatal(got)
	}
}

func TestKnownAgentOptionsKeepsDiscoverySnapshot(t *testing.T) {
	ports, catalog, port := defaultAgentPorts, defaultAgentCatalog, defaultAgentPort
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog, defaultAgentPort = ports, catalog, port })
	snapshot := map[string]string{"/alpha": "1001"}
	defaultAgentPorts = func(context.Context) map[string]string { return snapshot }
	defaultAgentPort = func(context.Context, string) string { return "1001" }
	defaultAgentCatalog = func(context.Context, string, string) []string { return nil }
	knownAgentOptions(context.Background(), []string{"/beta"})
	var wg sync.WaitGroup
	for i := range 25 {
		wg.Go(func() { knownAgentOptions(context.Background(), []string{fmt.Sprintf("/project-%d", i)}) })
	}
	wg.Wait()
	if len(snapshot) != 1 {
		t.Fatalf("discovery snapshot mutated: %v", snapshot)
	}
}

func TestKnownAgentOptionsFallsBackAfterCanceledPortDiscovery(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func(ctx context.Context) map[string]string {
		<-ctx.Done()
		return nil
	}
	defaultAgentCatalog = func(context.Context, string, string) []string {
		t.Fatal("catalog scheduled after port-discovery cancellation")
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := knownAgentOptions(ctx, []string{"/repo"}); !reflect.DeepEqual(got, []string{"build", "plan"}) {
		t.Fatal(got)
	}
}

func TestKnownAgentOptionsScopesDirectoriesSharingPort(t *testing.T) {
	ports, catalog, port := defaultAgentPorts, defaultAgentCatalog, defaultAgentPort
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog, defaultAgentPort = ports, catalog, port })
	defaultAgentPorts = func(context.Context) map[string]string { return map[string]string{"/alpha": "1001"} }
	defaultAgentPort = func(_ context.Context, directory string) string {
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
	defaultAgentPorts = func(context.Context) map[string]string {
		targets := make(map[string]string)
		for i := range 500 {
			targets[fmt.Sprintf("/project-%d", i)] = "1001"
		}
		return targets
	}
	var calls atomic.Int32
	defaultAgentCatalog = func(ctx context.Context, _, _ string) []string {
		calls.Add(1)
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := knownAgentOptions(ctx, nil); !reflect.DeepEqual(got, []string{"build", "plan"}) || calls.Load() > 8 {
		t.Fatalf("fallback = %v, catalog calls = %d, want at most eight calls", got, calls.Load())
	}
}

func TestKnownAgentOptionsIncludesHealthyCatalogBesideStalledTarget(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func(context.Context) map[string]string {
		return map[string]string{"/healthy": "1001", "/stalled": "1002"}
	}
	var started atomic.Int32
	bothStarted := make(chan struct{})
	defaultAgentCatalog = func(ctx context.Context, port, _ string) []string {
		if started.Add(1) == 2 {
			close(bothStarted)
		}
		select {
		case <-bothStarted:
			if port == "1001" {
				return []string{"healthy-agent"}
			}
		case <-ctx.Done():
			return nil
		}
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := knownAgentOptions(ctx, nil); !reflect.DeepEqual(got, []string{"build", "healthy-agent", "plan"}) {
		t.Fatalf("stalled target hid a healthy catalog: %v", got)
	}
}

func TestKnownAgentOptionsReadsHealthyTargetBeyondStalledBatch(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func(context.Context) map[string]string {
		targets := map[string]string{"/z-healthy": "healthy"}
		for i := range 8 {
			targets[fmt.Sprintf("/a-stalled-%d", i)] = "stalled"
		}
		return targets
	}
	defaultAgentCatalog = func(ctx context.Context, port, _ string) []string {
		if port == "healthy" {
			return []string{"healthy-agent"}
		}
		<-ctx.Done()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := knownAgentOptions(ctx, nil)
	if ctx.Err() != nil || !reflect.DeepEqual(got, []string{"build", "healthy-agent", "plan"}) {
		t.Fatalf("stalled batch exhausted the healthy target's budget: agents=%v error=%v", got, ctx.Err())
	}
}

func TestKnownAgentOptionsSetsOverallDeadline(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	defaultAgentPorts = func(context.Context) map[string]string { return map[string]string{"/repo": "1001"} }
	bounded := false
	defaultAgentCatalog = func(ctx context.Context, _, _ string) []string {
		deadline, ok := ctx.Deadline()
		bounded = ok && time.Until(deadline) <= 5*time.Second
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
