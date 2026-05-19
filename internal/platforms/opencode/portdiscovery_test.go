package opencode

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestParseOpenCodeListeners_MatchesAcrossBinaryRenames pins the
// COMMAND-column matching used by the lsof scan. OpenCode v1 ships as
// a plain "opencode" binary; v2 ships as a Bun-bundled single-file
// "opencode.exe", which lsof truncates to "opencode." in its 9-char
// COMMAND column. Both must produce a (pid, port) candidate, while
// unrelated processes ("opencod" prefix-miss, "ocaml") must be ignored.
func TestParseOpenCodeListeners_MatchesAcrossBinaryRenames(t *testing.T) {
	// Real-shaped lsof output: COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME.
	const sample = `COMMAND     PID USER   FD   TYPE             DEVICE SIZE/OFF NODE NAME
opencode  25767 dries   12u  IPv4 0xca568d85139c12c0      0t0  TCP 127.0.0.1:64813 (LISTEN)
opencode. 38828 dries   12u  IPv4 0xe608d6089558afa3      0t0  TCP 127.0.0.1:4096 (LISTEN)
opencod   11111 dries   12u  IPv4 0x1111111111111111      0t0  TCP 127.0.0.1:9000 (LISTEN)
ocaml     22222 dries    9u  IPv4 0x2222222222222222      0t0  TCP 127.0.0.1:9001 (LISTEN)
opencode  notapid dries 9u  IPv4 0x3333333333333333      0t0  TCP 127.0.0.1:9002 (LISTEN)
`

	got := parseOpenCodeListeners(sample)

	want := map[string]string{
		"25767": "64813",
		"38828": "4096",
	}
	if len(got) != len(want) {
		t.Fatalf("parseOpenCodeListeners returned %d candidates, want %d: %+v", len(got), len(want), got)
	}
	for _, c := range got {
		port, ok := want[c.pid]
		if !ok {
			t.Errorf("unexpected pid %q in result", c.pid)
			continue
		}
		if c.port != port {
			t.Errorf("pid %q: got port %q, want %q", c.pid, c.port, port)
		}
	}
}

// TestDiscoverOpenCodePorts_SingleflightsConcurrentMisses is the
// headline B3 contract: when N goroutines hit a cold cache at once,
// the underlying lsof scan must run exactly once. Without
// singleflight, the existing lock-around-lsof pattern serializes
// callers but still pays the lsof cost for every staggered miss
// (anyone whose check landed before the cache was filled).
func TestDiscoverOpenCodePorts_SingleflightsConcurrentMisses(t *testing.T) {
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		atomic.AddInt32(&calls, 1)
		// Slow the call enough that all goroutines pile onto the
		// same in-flight singleflight slot. Without singleflight
		// the test would observe up to N calls.
		time.Sleep(50 * time.Millisecond)
		return map[string]string{"/repo/a": "1234"}
	})
	defer restore()
	resetPortCacheForTests()

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
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		atomic.AddInt32(&calls, 1)
		return map[string]string{"/x": "9999"}
	})
	defer restore()
	resetPortCacheForTests()

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
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{"/repo": "5555"}
	})
	defer restore()
	resetPortCacheForTests()

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
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		atomic.AddInt32(&calls, 1)
		return map[string]string{"/repo/known": "1234"}
	})
	defer restore()
	resetPortCacheForTests()

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
