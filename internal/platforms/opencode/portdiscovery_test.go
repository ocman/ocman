package opencode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestPidCwd_ProcFsLinux verifies that pidCwd reads the cwd via /proc
// on Linux without spawning a second lsof process.
func TestPidCwd_ProcFsLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("proc-based fast path is Linux-only")
	}

	// Use our own PID; /proc/<self>/cwd is always available.
	pid := fmt.Sprintf("%d", os.Getpid())
	dir, ok := pidCwd(pid)
	if !ok {
		t.Fatal("pidCwd returned false for own PID on Linux")
	}
	// The returned path should be an absolute directory that exists.
	if !filepath.IsAbs(dir) {
		t.Errorf("pidCwd returned non-absolute path %q", dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("pidCwd returned path that does not exist: %v", err)
	}
}

// TestPidCwd_InvalidPidReturnsFalse verifies that an invalid/non-existent
// PID returns (_, false) without panicking on all platforms.
func TestPidCwd_InvalidPidReturnsFalse(t *testing.T) {
	// PID "0" is reserved (kernel) and will not have an accessible cwd.
	dir, ok := pidCwd("0")
	if ok {
		t.Errorf("expected pidCwd(\"0\") to fail, got dir=%q", dir)
	}
}

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

func TestInvalidateOpenCodePortCache_ForcesFreshScan(t *testing.T) {
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			return map[string]string{}
		}
		return map[string]string{"/repo/new": "7777"}
	})
	defer restore()
	resetPortCacheForTests()

	if got := discoverOpenCodePort("/repo/new"); got != "" {
		t.Fatalf("first lookup got port %q, want empty", got)
	}
	if got := discoverOpenCodePort("/repo/new"); got != "" {
		t.Fatalf("cached lookup got port %q, want empty", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("lsof invoked %d times before invalidation, want 1", got)
	}

	InvalidateOpenCodePortCache()

	if got := discoverOpenCodePort("/repo/new"); got != "7777" {
		t.Fatalf("post-invalidation lookup got port %q, want 7777", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("lsof invoked %d times after invalidation, want 2", got)
	}
}

func TestDiscoverOpenCodePortFresh_DoesNotCacheMissButCachesHit(t *testing.T) {
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			return map[string]string{}
		}
		return map[string]string{"/repo/new": "7777"}
	})
	defer restore()
	resetPortCacheForTests()

	if got := discoverOpenCodePortFresh("/repo/new"); got != "" {
		t.Fatalf("first fresh lookup got port %q, want empty", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("lsof invoked %d times after first lookup, want 1", got)
	}

	if got := discoverOpenCodePortFresh("/repo/new"); got != "7777" {
		t.Fatalf("second fresh lookup got port %q, want 7777", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("lsof invoked %d times after second lookup, want 2", got)
	}

	if got := discoverOpenCodePort("/repo/new"); got != "7777" {
		t.Fatalf("cached lookup after fresh hit got port %q, want 7777", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("cached lookup invoked lsof; calls = %d, want 2", got)
	}
}

func TestDiscoverOpenCodePortFresh_NormalizesSymlinkDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	linkDir := filepath.Join(root, "link")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{normalizePortDirectory(realDir): "7777"}
	})
	defer restore()
	resetPortCacheForTests()

	if got := discoverOpenCodePortFresh(linkDir); got != "7777" {
		t.Fatalf("fresh lookup through symlink got port %q, want 7777", got)
	}
	if got := discoverOpenCodePort(linkDir); got != "7777" {
		t.Fatalf("cached lookup through symlink got port %q, want 7777", got)
	}
	if got := discoverOpenCodePortCtx(context.Background(), linkDir); got != "7777" {
		t.Fatalf("ctx lookup through symlink got port %q, want 7777", got)
	}
}

func TestDuplicateOpenCodeServerPortsForDirectory(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	linkDir := filepath.Join(root, "link")
	otherDir := filepath.Join(root, "other")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real dir: %v", err)
	}
	if err := os.Mkdir(otherDir, 0o755); err != nil {
		t.Fatalf("mkdir other dir: %v", err)
	}
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Fatalf("symlink dir: %v", err)
	}

	servers := []openCodeServer{
		{directory: normalizePortDirectory(realDir), port: "55001"},
		{directory: normalizePortDirectory(linkDir), port: "55002"},
		{directory: normalizePortDirectory(realDir), port: "55002"},
		{directory: normalizePortDirectory(otherDir), port: "55003"},
	}

	got := duplicateOpenCodeServerPortsForDirectory(linkDir, servers)
	want := []string{"55001", "55002"}
	if len(got) != len(want) {
		t.Fatalf("duplicate ports = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("duplicate ports = %v, want %v", got, want)
		}
	}

	if got := duplicateOpenCodeServerPortsForDirectory(otherDir, servers); got != nil {
		t.Fatalf("single server duplicate ports = %v, want nil", got)
	}
}

func TestDiscoverDuplicateOpenCodeServerPorts_CachesServerDiscovery(t *testing.T) {
	const dir = "/repo/a"

	var calls int32
	restore := setDiscoverServersImplForTests(func() []openCodeServer {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			return []openCodeServer{
				{directory: normalizePortDirectory(dir), port: "1111"},
				{directory: normalizePortDirectory(dir), port: "2222"},
			}
		}
		return []openCodeServer{
			{directory: normalizePortDirectory(dir), port: "3333"},
			{directory: normalizePortDirectory(dir), port: "4444"},
		}
	})
	defer restore()
	resetPortCacheForTests()
	defer resetPortCacheForTests()

	if got := discoverDuplicateOpenCodeServerPorts(dir); !slices.Equal(got, []string{"1111", "2222"}) {
		t.Fatalf("first duplicate ports = %v, want [1111 2222]", got)
	}
	if got := discoverDuplicateOpenCodeServerPorts(dir); !slices.Equal(got, []string{"1111", "2222"}) {
		t.Fatalf("cached duplicate ports = %v, want [1111 2222]", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("server discovery calls before invalidation = %d, want 1", got)
	}

	InvalidateOpenCodePortCache()

	if got := discoverDuplicateOpenCodeServerPorts(dir); !slices.Equal(got, []string{"3333", "4444"}) {
		t.Fatalf("post-invalidation duplicate ports = %v, want [3333 4444]", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("server discovery calls after invalidation = %d, want 2", got)
	}
}

func TestResolveOpenCodePortForSessionCtx_PinsSessionPort(t *testing.T) {
	var calls int32
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		call := atomic.AddInt32(&calls, 1)
		if call == 1 {
			return map[string]string{"/repo/a": "1111"}
		}
		return map[string]string{"/repo/a": "2222"}
	})
	defer restore()
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	defer resetSessionPortAffinityForTests()

	if got := resolveOpenCodePortForSessionCtx(context.Background(), "sess-1", "/repo/a"); got != "1111" {
		t.Fatalf("first lookup got port %q, want 1111", got)
	}

	InvalidateOpenCodePortCache()
	if got := resolveOpenCodePortForSessionCtx(context.Background(), "sess-1", "/repo/a"); got != "1111" {
		t.Fatalf("same-session lookup after cache invalidation got port %q, want pinned 1111", got)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("same-session affinity invoked lsof %d times, want 1", got)
	}

	if got := resolveOpenCodePortForSessionCtx(context.Background(), "sess-2", "/repo/a"); got != "2222" {
		t.Fatalf("different session lookup got port %q, want fresh 2222", got)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("fresh session invoked lsof %d times, want 2", got)
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

// TestDiscoverOpenCodePorts_Exported is a smoke test for the exported
// wrapper used by the headless auto-approve watcher. It must return the
// full directory -> port map and a mutation by the caller must not leak
// into subsequent calls (same isolation guarantee as the unexported
// path, which the watcher relies on so a faulty caller can't poison
// the cache).
func TestDiscoverOpenCodePorts_Exported(t *testing.T) {
	restore := setDiscoverPortsImplForTests(func() map[string]string {
		return map[string]string{
			"/repo/a": "1111",
			"/repo/b": "2222",
		}
	})
	defer restore()
	resetPortCacheForTests()

	got := DiscoverOpenCodePorts()
	if got["/repo/a"] != "1111" || got["/repo/b"] != "2222" {
		t.Errorf("DiscoverOpenCodePorts: got %v, want a=1111 b=2222", got)
	}

	got["/repo/a"] = "MUTATED"
	again := DiscoverOpenCodePorts()
	if again["/repo/a"] != "1111" {
		t.Errorf("caller mutation leaked into cache: %v", again)
	}
}
