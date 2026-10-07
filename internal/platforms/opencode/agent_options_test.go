package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"sync"
	"sync/atomic"
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

func TestAgentNamesCanceledLeaderPreservesComposerCatalog(t *testing.T) {
	started, release, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
			writeJSONBody(w, `[{"name":"custom-agent","mode":"primary"}]`)
		case <-r.Context().Done():
			close(canceled)
		}
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	t.Cleanup(func() { catalogCache.invalidatePort(u.Port()) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leader := make(chan []string, 1)
	go func() { leader <- AgentNames(ctx, u.Port(), "/repo") }()
	<-started
	key := u.Port() + "|" + scopedPath(context.Background(), u.Port(), "/agent", "/repo")
	waiter := catalogCache.flight.DoChan(key, func() (any, error) {
		t.Error("composer did not join the in-flight catalog read")
		return nil, errFetchFailed
	})
	cancel()
	if names := <-leader; len(names) != 0 {
		t.Error(names)
	}
	select {
	case <-canceled:
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if result := <-waiter; result.Err != nil {
		t.Fatalf("Settings cancellation failed the composer's shared fetch: %v", result.Err)
	}
	agents := agentCatalogAt(context.Background(), u.Port(), "composer", "/repo")
	if len(agents) != 1 || agents[0].Name != "custom-agent" {
		t.Fatalf("composer lost its agent catalog: %v", agents)
	}
}

func TestAgentNamesBoundsUnderlyingReadsAfterCallersCancel(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	const callers = 24
	var active, peak atomic.Int32
	release := make(chan struct{})
	completed := make(chan struct{}, callers)
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/agent" {
			return false
		}
		current := active.Add(1)
		defer func() { active.Add(-1); completed <- struct{}{} }()
		for previous := peak.Load(); current > previous; previous = peak.Load() {
			if peak.CompareAndSwap(previous, current) {
				break
			}
		}
		select {
		case <-release:
			writeJSONBody(w, `{"data":[{"id":"custom","mode":"primary"}]}`)
		case <-r.Context().Done():
		}
		return true
	})
	t.Cleanup(func() { catalogCache.invalidatePort(f.Port()) })
	var waiters sync.WaitGroup
	for i := range callers {
		waiters.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			AgentNames(ctx, f.Port(), fmt.Sprintf("/scope/%d", i))
		})
	}
	waiters.Wait()
	observed := peak.Load()
	close(release)
	for range callers {
		select {
		case <-completed:
		case <-time.After(5 * time.Second):
			t.Fatal("catalog reads did not settle after releasing the upstream")
		}
	}
	for i := range callers {
		path := scopedPath(context.Background(), f.Port(), "/agent", fmt.Sprintf("/scope/%d", i))
		<-catalogCache.flight.DoChan(f.Port()+"|"+path, func() (any, error) { return nil, nil })
	}
	if observed > 8 {
		t.Fatalf("canceled callers left %d underlying agent reads active, want at most eight", observed)
	}
}

func TestAgentNamesReadsHealthyScopeAfterFullStalledFetchBatch(t *testing.T) {
	defer ocv2.SetInstalledV2(true)()
	started := make(chan struct{}, 8)
	f := newV2Fake(t, true, func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/agent" {
			return false
		}
		if r.URL.Query().Get("location[directory]") == "/healthy" {
			writeJSONBody(w, `{"data":[{"id":"healthy-agent","mode":"primary"}]}`)
		} else {
			started <- struct{}{}
			<-r.Context().Done()
		}
		return true
	})
	t.Cleanup(func() { catalogCache.invalidatePort(f.Port()) })
	var stalled sync.WaitGroup
	for i := range 8 {
		stalled.Go(func() { AgentNames(context.Background(), f.Port(), fmt.Sprintf("/stalled/%d", i)) })
	}
	for range 8 {
		<-started
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	got := AgentNames(ctx, f.Port(), "/healthy")
	stalled.Wait()
	if !reflect.DeepEqual(got, []string{"healthy-agent"}) || ctx.Err() != nil {
		t.Fatalf("stalled fetch batch starved healthy scope: agents=%v error=%v", got, ctx.Err())
	}
}

func TestAgentCatalogAdmissionTimeoutDoesNotCacheFailure(t *testing.T) {
	for range cap(agentCatalogFetchSlots) {
		agentCatalogFetchSlots <- struct{}{}
	}
	defer func() {
		for range cap(agentCatalogFetchSlots) {
			<-agentCatalogFetchSlots
		}
	}()
	const port = "admission-timeout-test"
	t.Cleanup(func() { catalogCache.invalidatePort(port) })
	if _, err := getJSONCached(context.Background(), port, "/agent"); err == nil {
		t.Fatal("full agent fetch pool did not time out admission")
	}
	if _, cached := catalogCache.get(port, "/agent"); cached {
		t.Fatal("admission failure was cached")
	}
}
