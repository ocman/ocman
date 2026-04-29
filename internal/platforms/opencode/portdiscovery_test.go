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

// TestDiscoverOpenCodePort_UnknownDirDoesNotInvalidateCache guards
// against a previous behaviour where a singular lookup for a
// directory not in the cache invalidated the entire port cache and
// forced a second lsof scan. That made every viewer of a session
// whose OpenCode instance had stopped pay 2× lsof per request, AND
// poisoned the cache for every other concurrent caller (dashboard,
// info, models, ...). The cache TTL (3s) is short enough that
// genuinely new instances are picked up quickly without this
// thrash.
func TestDiscoverOpenCodePort_UnknownDirDoesNotInvalidateCache(t *testing.T) {
	prev := discoverPortsImpl
	defer func() { discoverPortsImpl = prev }()

	resetPortCacheForTests()

	var calls int32
	discoverPortsImpl = func() map[string]string {
		atomic.AddInt32(&calls, 1)
		return map[string]string{"/repo/known": "1234"}
	}

	// First call warms the cache; lsof runs once.
	if got := discoverOpenCodePort("/repo/unknown"); got != "" {
		t.Errorf("unknown dir: got port %q, want empty", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("lsof invoked %d times on cold call, want 1", got)
	}

	// Subsequent lookups for unknown directories must reuse the
	// warm cache, not invalidate it. With the bug, this would
	// trigger a second lsof.
	for i := 0; i < 5; i++ {
		if got := discoverOpenCodePort("/repo/still-unknown"); got != "" {
			t.Errorf("iteration %d: got %q, want empty", i, got)
		}
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("lsof invoked %d times after misses, want 1", got)
	}

	// Sanity: a known directory still resolves from the warm cache.
	if got := discoverOpenCodePort("/repo/known"); got != "1234" {
		t.Errorf("known dir: got %q, want 1234", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Errorf("lsof invoked %d times after known-dir hit, want 1", got)
	}
}
