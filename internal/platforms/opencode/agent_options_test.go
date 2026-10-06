package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

func TestAgentOptionsDoNotDependOnProviders(t *testing.T) {
	providerCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent" {
			writeJSONBody(w, `[{"name":"custom-agent","mode":"primary"},{"name":"shared-agent","mode":"all"},{"name":"helper","mode":"subagent"},{"name":"title","hidden":true},{"name":""}]`)
			return
		}
		providerCalls++
		http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	t.Cleanup(func() { catalogCache.invalidatePort(u.Port()) })
	agents := AgentNames(context.Background(), u.Port(), "/repo")
	if !reflect.DeepEqual(agents, []string{"custom-agent", "shared-agent"}) || providerCalls != 0 {
		t.Fatalf("provider failure discarded agents: %v", agents)
	}
}

func TestAgentNamesScopesV2ProjectsOnSharedServer(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/agent" {
			return false
		}
		directory := r.URL.Query().Get("location[directory]")
		name := map[string]string{"/alpha": "alpha-agent", "/beta": "beta-agent"}[directory]
		writeJSONBody(w, `{"data":[{"id":"`+name+`","mode":"primary"}]}`)
		return true
	})
	t.Cleanup(func() { catalogCache.invalidatePort(f.Port()) })
	for directory, name := range map[string]string{"/alpha": "alpha-agent", "/beta": "beta-agent"} {
		if got := AgentNames(context.Background(), f.Port(), directory); !reflect.DeepEqual(got, []string{name}) {
			t.Fatalf("%s: got %v, want %s", directory, got, name)
		}
	}
}

func TestAgentNamesCancellationWhileCacheFetchIsInFlight(t *testing.T) {
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	const port = "agent-cancel-test"
	t.Cleanup(func() { catalogCache.invalidatePort(port) })
	go func() {
		catalogCache.getOrFetch(port, "/agent", func() ([]byte, bool) {
			close(started)
			<-release
			return []byte(`[{"name":"custom","mode":"primary"}]`), true
		})
		close(finished)
	}()
	<-started
	defer func() { close(release); <-finished }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan []string, 1)
	go func() { result <- AgentNames(ctx, port, "/repo") }()
	select {
	case names := <-result:
		if len(names) != 0 {
			t.Fatal(names)
		}
	case <-time.After(time.Second):
		t.Fatal("agent read ignored cancellation while waiting on the shared cache")
	}
}
