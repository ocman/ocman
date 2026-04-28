package opencode

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDiscoverOpenCodePorts_SingleflightsConcurrentMisses is the
// headline B3 contract: when N goroutines hit a cold cache at once,
// the underlying lsof scan must run exactly once. Without
// singleflight, the existing lock-around-lsof pattern serializes
// callers but still pays the lsof cost for every staggered miss
// (anyone whose check landed before the cache was filled).
func TestDiscoverOpenCodePorts_SingleflightsConcurrentMisses(t *testing.T) {
	prev := discoverPortsImpl
	defer func() { discoverPortsImpl = prev }()

	resetPortCacheForTests()

	var calls int32
	discoverPortsImpl = func() map[string]string {
		atomic.AddInt32(&calls, 1)
		// Slow the call enough that all goroutines pile onto the
		// same in-flight singleflight slot. Without singleflight
		// the test would observe up to N calls.
		time.Sleep(50 * time.Millisecond)
		return map[string]string{"/repo/a": "1234"}
	}

	const concurrency = 25
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			ports := discoverOpenCodePorts()
			if ports["/repo/a"] != "1234" {
				t.Errorf("missing port: got %v", ports)
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("lsof invoked %d times, want 1", got)
	}
}

// TestDiscoverOpenCodePorts_CacheHitDoesNotRunLsof verifies that the
// TTL cache short-circuits the singleflight entirely — within the
// TTL window we should never enter the lsof path at all.
func TestDiscoverOpenCodePorts_CacheHitDoesNotRunLsof(t *testing.T) {
	prev := discoverPortsImpl
	defer func() { discoverPortsImpl = prev }()

	resetPortCacheForTests()

	var calls int32
	discoverPortsImpl = func() map[string]string {
		atomic.AddInt32(&calls, 1)
		return map[string]string{"/x": "9999"}
	}

	_ = discoverOpenCodePorts()
	_ = discoverOpenCodePorts()
	_ = discoverOpenCodePorts()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("lsof invoked %d times within TTL, want 1", got)
	}
}

// TestDiscoverOpenCodePorts_CallersGetIsolatedCopies — mutating the
// returned map must not affect the cached data or other callers'
// copies. (Pre-existing copyMap behaviour; we keep the test so the
// singleflight refactor doesn't accidentally drop it.)
func TestDiscoverOpenCodePorts_CallersGetIsolatedCopies(t *testing.T) {
	prev := discoverPortsImpl
	defer func() { discoverPortsImpl = prev }()

	resetPortCacheForTests()

	discoverPortsImpl = func() map[string]string {
		return map[string]string{"/repo": "5555"}
	}

	a := discoverOpenCodePorts()
	b := discoverOpenCodePorts()

	a["/repo"] = "MUTATED"
	if b["/repo"] != "5555" {
		t.Errorf("mutation in caller A leaked into caller B: %v", b)
	}
	c := discoverOpenCodePorts()
	if c["/repo"] != "5555" {
		t.Errorf("mutation in caller A leaked into the cache: %v", c)
	}
}
