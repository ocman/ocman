package opencode

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"
)

func resetPortCacheForTests() {
	td.resetPortCache()
	td.portCache.mu.Lock()
	td.portCache.lastSuccessful = nil
	td.portCache.lastSuccessfulAt = time.Time{}
	td.portCache.mu.Unlock()
}

func resetSessionPortAffinityForTests() {
	td.sessionPortAffinity.Range(func(key, _ interface{}) bool {
		td.sessionPortAffinity.Delete(key)
		return true
	})
}

func TestFailedDiscoveryKeepsLastSnapshotWithoutRenewingTTL(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{"/repo": "1001"} })
	defer restore()
	if ports := td.discoverOpenCodePorts(); ports["/repo"] != "1001" {
		t.Fatal(ports)
	}
	expired := time.Now().Add(-2 * portCacheTTL)
	td.portCache.mu.Lock()
	td.portCache.updated = expired
	td.portCache.mu.Unlock()
	failed := func(context.Context) map[string]string { return nil }
	previous := discoverPortsOverride.Swap(&failed)
	defer discoverPortsOverride.Store(previous)
	if ports := td.DiscoverOpenCodePortsContext(context.Background()); ports != nil {
		t.Fatalf("failed scan returned a successful snapshot: %v", ports)
	}
	if port := td.DiscoverOpenCodePort("/repo"); port != "1001" {
		t.Fatalf("failed scan disconnected a known live instance: %s", port)
	}
	ports := td.discoverOpenCodePorts()
	ports["/another"] = "1002"
	td.portCache.mu.Lock()
	unchanged := td.portCache.updated.Equal(expired) && len(td.portCache.lastSuccessful) == 1
	td.portCache.mu.Unlock()
	if !unchanged {
		t.Fatal("fallback renewed the cache TTL or leaked a mutable snapshot")
	}
	empty := func(context.Context) map[string]string { return map[string]string{} }
	discoverPortsOverride.Store(&empty)
	if ports := td.discoverOpenCodePorts(); ports == nil || len(ports) != 0 {
		t.Fatalf("successful empty discovery retained disappeared instances: %v", ports)
	}
}

func TestFailedDiscoveryExpiresStoppedInstanceMembership(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{"/repo": "1001"} })
	defer restore()
	td.discoverOpenCodePorts()
	td.portCache.mu.Lock()
	td.portCache.updated = time.Now().Add(-2 * time.Minute)
	td.portCache.lastSuccessfulAt = td.portCache.updated
	td.portCache.mu.Unlock()
	failed := func(context.Context) map[string]string { return nil }
	previous := discoverPortsOverride.Swap(&failed)
	defer discoverPortsOverride.Store(previous)
	if ports := td.DiscoverOpenCodePorts(); len(ports) != 0 {
		t.Fatalf("sustained discovery failure retained stopped watcher members: %v", ports)
	}
}

func TestDiscoveryColdCallersOwnIndependentSnapshots(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		time.Sleep(50 * time.Millisecond)
		return map[string]string{"/repo": "1001"}
	})
	defer restore()
	const callers = 25
	snapshots := make([]map[string]string, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Go(func() { snapshots[i] = td.DiscoverOpenCodePortsContext(context.Background()) })
	}
	wg.Wait()
	snapshots[0]["/another"] = "1002"
	for _, snapshot := range snapshots[1:] {
		if len(snapshot) != 1 || snapshot["/repo"] != "1001" {
			t.Fatalf("cold-cache callers share a mutable map: %v", snapshot)
		}
	}
}

func TestDiscoveryWaiterCanCancelWithoutCancelingLeader(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		close(started)
		<-release
		return map[string]string{"/repo": "1001"}
	})
	defer restore()
	go func() { td.DiscoverOpenCodePortsContext(context.Background()); close(finished) }()
	<-started
	defer func() { close(release); <-finished }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan map[string]string, 1)
	go func() { result <- td.DiscoverOpenCodePortsContext(ctx) }()
	select {
	case ports := <-result:
		if len(ports) != 0 || ctx.Err() == nil {
			t.Fatalf("canceled waiter returned %v", ports)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled discovery ignored waiter cancellation")
	}
}

func TestDiscoveryCanceledLeaderDoesNotDisconnectWaiter(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	started, release := make(chan struct{}), make(chan struct{})
	fn := func(ctx context.Context) map[string]string {
		close(started)
		<-release
		if ctx.Err() != nil {
			return nil
		}
		return map[string]string{"/repo": "1001"}
	}
	previous := discoverPortsOverride.Swap(&fn)
	defer discoverPortsOverride.Store(previous)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	leader := make(chan map[string]string, 1)
	go func() { leader <- td.DiscoverOpenCodePortsContext(ctx) }()
	<-started
	waiterCtx := &signalingContext{Context: context.Background(), waiting: make(chan struct{})}
	waiter := make(chan map[string]string, 1)
	go func() { waiter <- td.DiscoverOpenCodePortsContext(waiterCtx) }()
	<-waiterCtx.waiting
	cancel()
	if ports := <-leader; len(ports) != 0 {
		t.Error(ports)
	}
	close(release)
	if ports := <-waiter; ports["/repo"] != "1001" {
		t.Fatalf("canceled leader disconnected an uncanceled waiter: %v", ports)
	}
}

func TestCanceledDiscoveryDoesNotPoisonCache(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	td.portCache.mu.Lock()
	flightKey := "discoverOpenCodePorts:" + strconv.FormatUint(td.portCache.generation, 10)
	td.portCache.mu.Unlock()
	fn := func(ctx context.Context) map[string]string { <-ctx.Done(); return nil }
	previous := discoverPortsOverride.Swap(&fn)
	defer discoverPortsOverride.Store(previous)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if ports := td.DiscoverOpenCodePortsContext(ctx); len(ports) != 0 {
		t.Fatal(ports)
	}
	// Join the canceled flight before inspecting its cache result.
	<-td.portFlight.DoChan(flightKey, func() (any, error) { return nil, nil })
	if _, cached := td.readCachedPorts(); cached {
		t.Fatal("canceled discovery cached an empty result")
	}
	if port := td.DiscoverOpenCodePortContext(ctx, "/repo"); port != "" {
		t.Fatal(port)
	}
}
