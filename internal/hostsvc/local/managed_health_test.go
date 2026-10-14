package local

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

func TestManagedPortReadDoesNotProbe(t *testing.T) {
	rt := inventoryRuntime{fakeRuntime: &fakeRuntime{}, probeFn: func(ctx context.Context, _ *ocruntime.Instance) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	h := New(Deps{Runtime: rt})
	h.authorizeInstance("/repo", &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001"})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if ports := h.ManagedOpenCodePorts(ctx); ports["/repo"] != "1001" || ctx.Err() != nil {
		t.Fatalf("healthy runtime lookup blocked on probe: ports=%v error=%v", ports, ctx.Err())
	}
}

func TestHealthScanBoundedConcurrentAndJoined(t *testing.T) {
	started := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var active atomic.Int32
	rt := inventoryRuntime{fakeRuntime: &fakeRuntime{}, probeFn: func(ctx context.Context, _ *ocruntime.Instance) error {
		active.Add(1)
		defer active.Add(-1)
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 2*time.Second {
			t.Error("probe has no short deadline")
		}
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}}
	h := New(Deps{Runtime: rt})
	for i := range 8 {
		h.authorizeInstance(fmt.Sprintf("/repo%d", i), &ocruntime.Instance{Endpoint: "http://127.0.0.1:1001"})
	}
	done := make(chan struct{})
	go func() { h.RefreshManagedOpenCodePorts(ctx); close(done) }()
	for range 4 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("probes did not run concurrently")
		}
	}
	select {
	case <-started:
		t.Fatal("scan exceeded four concurrent probes")
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scan did not join cancelled probes")
	}
	if active.Load() != 0 {
		t.Fatal("probe outlived scan")
	}
	if ports := h.ManagedOpenCodePorts(t.Context()); len(ports) != 8 {
		t.Fatal("cancellation revoked routing proof", ports)
	}
}

func TestHealthWorkerStartupAndPeriodicRecovery(t *testing.T) {
	store := newFakeStore()
	store.rows["/repo"] = ManagedInstance{Endpoint: "http://127.0.0.1:1001"}
	var dead atomic.Bool
	changed := make(chan struct{}, 4)
	rt := inventoryRuntime{fakeRuntime: &fakeRuntime{}, probeFn: func(context.Context, *ocruntime.Instance) error {
		if dead.Load() {
			return ocruntime.ErrProbeUnreachable
		}
		return nil
	}}
	h := New(Deps{Runtime: rt, ManagedStore: store, RuntimeInventoryChanged: func() { changed <- struct{}{} }})
	if ports := h.ManagedOpenCodePorts(t.Context()); len(ports) != 0 {
		t.Fatal("cold inventory was trusted without a probe", ports)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() { h.runOpenCodeHealthChecks(ctx, ticks); close(done) }()
	waitChange := func() {
		t.Helper()
		select {
		case <-changed:
		case <-time.After(time.Second):
			t.Fatal("worker missed scan")
		}
	}
	waitChange()
	if ports := h.ManagedOpenCodePorts(t.Context()); ports["/repo"] != "1001" {
		t.Fatal("startup did not recover healthy persisted runtime", ports)
	}
	dead.Store(true)
	ticks <- time.Now()
	waitChange()
	if ports := h.ManagedOpenCodePorts(t.Context()); len(ports) != 0 || h.currentInstance("/repo") == nil {
		t.Fatal("dead runtime remained routable or lost cleanup handle", ports)
	}
	dead.Store(false)
	ticks <- time.Now()
	waitChange()
	if ports := h.ManagedOpenCodePorts(t.Context()); ports["/repo"] != "1001" {
		t.Fatal("recovered runtime stayed hidden", ports)
	}
	cancel()
	<-done
	if rt.launchCount() != 0 || rt.stopCount() != 0 {
		t.Fatal("health worker mutated runtime")
	}
}

func TestHealthWorkerStopsAfterStartupFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	h := New(Deps{ManagedStore: failingListStore{}})
	done := make(chan struct{})
	go func() { h.RunOpenCodeHealthChecks(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	if ports := h.ManagedOpenCodePorts(ctx); ports != nil {
		t.Fatal(ports)
	}
	h.deps.PlatformEnabled = func(string) bool { return false }
	if ports := h.ManagedOpenCodePorts(t.Context()); ports == nil || len(ports) != 0 {
		t.Fatal(ports)
	}
}

func TestHealthScanPublishesOnlyV2MachineServer(t *testing.T) {
	h, rt, store, rec, root := v2Host(t)
	store.rows[root] = ManagedInstance{Endpoint: "http://127.0.0.1:7777"}
	store.rows["/old-v1"] = ManagedInstance{Endpoint: "http://127.0.0.1:1001"}
	ports := h.RefreshManagedOpenCodePorts(t.Context())
	if len(ports) != 1 || ports[root] != "7777" || rec.last() != "7777" {
		t.Fatalf("machine recovery published stale runtimes: %v, machine=%q", ports, rec.last())
	}
	rt.probe = func(*ocruntime.Instance) bool { return false }
	if ports := h.RefreshManagedOpenCodePorts(t.Context()); len(ports) != 0 || rec.last() != "" {
		t.Fatalf("dead machine stayed published: %v, machine=%q", ports, rec.last())
	}
}

func TestLaunchImmediatelyVisibleToCachedDiscovery(t *testing.T) {
	disc := opencode.NewDiscovery()
	h := New(Deps{Runtime: &fakeRuntime{endpoint: "http://127.0.0.1:1002"}, RuntimeInventoryChanged: disc.InvalidateOpenCodePortCache})
	disc.SetManagedPortSource(h.ManagedOpenCodePorts)
	if ports := disc.DiscoverOpenCodePortsContext(t.Context()); len(ports) != 0 {
		t.Fatal(ports)
	}
	result, err := h.EnsureProjectOpencode(t.Context(), hostsvc.EnsureProjectOpencodeRequest{ProjectDir: initRepo(t)})
	if err != nil {
		t.Fatal(err)
	}
	if port := disc.DiscoverOpenCodePortContext(t.Context(), result.RepoRoot); port != "1002" {
		t.Fatalf("launched server missing from cached discovery: %q", port)
	}
}
