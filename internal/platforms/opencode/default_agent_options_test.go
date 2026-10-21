package opencode

import (
	"context"
	"fmt"
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
	if got := knownAgentOptions(context.Background(), nil, nil); !reflect.DeepEqual(got, []string{"build", "custom", "plan", "review"}) {
		t.Fatal(got)
	}
	defaultAgentPorts = func(context.Context) map[string]string { return nil }
	if got := knownAgentOptions(context.Background(), nil, nil); !reflect.DeepEqual(got, []string{"build", "plan"}) {
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
	knownAgentOptions(context.Background(), nil, []string{"/beta"})
	var wg sync.WaitGroup
	for i := range 25 {
		wg.Go(func() { knownAgentOptions(context.Background(), nil, []string{fmt.Sprintf("/project-%d", i)}) })
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
	if got := knownAgentOptions(ctx, nil, []string{"/repo"}); !reflect.DeepEqual(got, []string{"build", "plan"}) {
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
	if got := knownAgentOptions(context.Background(), nil, []string{"/alpha", "/beta", "/offline"}); !reflect.DeepEqual(got, []string{"alpha-agent", "beta-agent", "build", "plan"}) {
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
	if got := knownAgentOptions(ctx, nil, nil); !reflect.DeepEqual(got, []string{"build", "plan"}) || calls.Load() > 8 {
		t.Fatalf("fallback = %v, catalog calls = %d, want at most eight calls", got, calls.Load())
	}
}

// shortenDefaultAgentTargetTimeout keeps stalled-target tests from waiting
// out the production per-target budget.
func shortenDefaultAgentTargetTimeout(t *testing.T) {
	previous := defaultAgentTargetTimeout
	if previous != 4*time.Second {
		t.Fatalf("defaultAgentTargetTimeout = %v, want 4s", previous)
	}
	t.Cleanup(func() { defaultAgentTargetTimeout = previous })
	defaultAgentTargetTimeout = 100 * time.Millisecond
}

func TestKnownAgentOptionsIncludesHealthyCatalogBesideStalledTarget(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	shortenDefaultAgentTargetTimeout(t)
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
	if got := knownAgentOptions(ctx, nil, nil); !reflect.DeepEqual(got, []string{"build", "healthy-agent", "plan"}) {
		t.Fatalf("stalled target hid a healthy catalog: %v", got)
	}
}

func TestKnownAgentOptionsReadsHealthyTargetBeyondStalledBatch(t *testing.T) {
	ports, catalog := defaultAgentPorts, defaultAgentCatalog
	t.Cleanup(func() { defaultAgentPorts, defaultAgentCatalog = ports, catalog })
	shortenDefaultAgentTargetTimeout(t)
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
	got := knownAgentOptions(ctx, nil, nil)
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
	knownAgentOptions(context.Background(), nil, nil)
	if !bounded {
		t.Fatal("agent discovery has no overall deadline")
	}
}
