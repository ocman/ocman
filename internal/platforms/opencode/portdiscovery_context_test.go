package opencode

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocv2"
)

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
		wg.Go(func() { snapshots[i] = DiscoverOpenCodePortsContext(context.Background()) })
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
	go func() { DiscoverOpenCodePortsContext(context.Background()); close(finished) }()
	<-started
	defer func() { close(release); <-finished }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	result := make(chan map[string]string, 1)
	go func() { result <- DiscoverOpenCodePortsContext(ctx) }()
	select {
	case ports := <-result:
		if len(ports) != 0 || ctx.Err() == nil {
			t.Fatalf("canceled waiter returned %v", ports)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled discovery ignored waiter cancellation")
	}
}

func TestCanceledDiscoveryDoesNotPoisonCache(t *testing.T) {
	resetPortCacheForTests()
	t.Cleanup(resetPortCacheForTests)
	fn := func(ctx context.Context) map[string]string { <-ctx.Done(); return nil }
	previous := discoverPortsImpl.Swap(&fn)
	defer discoverPortsImpl.Store(previous)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if ports := DiscoverOpenCodePortsContext(ctx); len(ports) != 0 {
		t.Fatal(ports)
	}
	// Join the canceled flight before inspecting its cache result.
	<-portFlight.DoChan("discoverOpenCodePorts", func() (any, error) { return nil, nil })
	if _, cached := readCachedPorts(); cached {
		t.Fatal("canceled discovery cached an empty result")
	}
	if port := DiscoverOpenCodePortContext(ctx, "/repo"); port != "" {
		t.Fatal(port)
	}
}

func TestDiscoveryCancelsListenerAndCwdSubprocesses(t *testing.T) {
	defer ocv2.SetInstalledV2(false)()
	for _, tc := range []struct {
		name, script string
	}{
		{"listeners", "#!/bin/sh\nexec sleep 30\n"},
		{"directories", "#!/bin/sh\nif [ \"$1\" = \"-iTCP\" ]; then\n  i=0; while [ $i -lt 20 ]; do\n    printf 'opencode 999999 user 1u IPv4 0t0 TCP 127.0.0.1:1001 (LISTEN)\\n'\n    i=$((i+1))\n  done\nelse\n  exec sleep 30\nfi\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "lsof"), []byte(tc.script), 0o700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			start := time.Now()
			if servers := discoverOpenCodeServersUncachedContext(ctx); len(servers) != 0 || ctx.Err() == nil {
				t.Fatalf("canceled discovery returned %v", servers)
			}
			if time.Since(start) > time.Second {
				t.Fatal("lsof subprocess did not stop after cancellation")
			}
		})
	}
}
