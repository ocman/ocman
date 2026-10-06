package opencode

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// portCache holds cached port discovery results with a single mutex
// for simplicity; port discovery is infrequent enough that read/write
// contention is not a concern.
var portCache struct {
	mu      sync.Mutex
	ports   map[string]string
	updated time.Time
}

var serverCache struct {
	mu      sync.Mutex
	servers []openCodeServer
	updated time.Time
}

var sessionPortAffinity sync.Map // sessionID -> port

// portFlight coalesces concurrent cold-cache callers into a single
// underlying lsof invocation.
var portFlight singleflight.Group

// serverFlight coalesces concurrent cold-cache callers that need the
// full server list rather than the directory -> port map.
var serverFlight singleflight.Group

// portCacheTTL bounds how long a discovered port map is reused
// before the next request triggers a fresh lsof scan.
const portCacheTTL = 10 * time.Second

// discoverPortsImpl is the seam used so tests can swap out the expensive
// lsof execution. Stored as an atomic pointer so reads and writes are
// race-free without a mutex.
var discoverPortsImpl atomic.Pointer[func(context.Context) map[string]string]
var discoverServersImpl atomic.Pointer[func() []openCodeServer]

func init() {
	serverFn := func() []openCodeServer { return discoverOpenCodeServersUncached() }
	discoverServersImpl.Store(&serverFn)

	fn := discoverOpenCodePortsUncachedContext
	discoverPortsImpl.Store(&fn)
}

// setDiscoverPortsImplForTests installs fn as the seam and returns a
// restore func that re-installs the previous value.
func setDiscoverPortsImplForTests(fn func() map[string]string) func() {
	wrapper := func(context.Context) map[string]string { return fn() }
	prev := discoverPortsImpl.Swap(&wrapper)
	return func() { discoverPortsImpl.Store(prev) }
}

func setDiscoverServersImplForTests(fn func() []openCodeServer) func() {
	prev := discoverServersImpl.Swap(&fn)
	return func() { discoverServersImpl.Store(prev) }
}

// resetPortCache clears the shared port-discovery caches.
func resetPortCache() {
	portCache.mu.Lock()
	portCache.ports = nil
	portCache.updated = time.Time{}
	portCache.mu.Unlock()

	serverCache.mu.Lock()
	serverCache.servers = nil
	serverCache.updated = time.Time{}
	serverCache.mu.Unlock()
}

// InvalidateOpenCodePortCache clears cached OpenCode port-discovery data.
// Callers that have just launched or restarted an OpenCode process should
// invalidate so the next lookup does a fresh lsof scan instead of reusing
// a recently cached "not running yet" result.
func InvalidateOpenCodePortCache() {
	resetPortCache()
}

// resetPortCacheForTests clears the cache so each test starts with a cold path.
func resetPortCacheForTests() {
	resetPortCache()
}

func resetSessionPortAffinityForTests() {
	sessionPortAffinity.Range(func(key, _ interface{}) bool {
		sessionPortAffinity.Delete(key)
		return true
	})
}

func rememberSessionPort(sessionID, port string) {
	if sessionID == "" || port == "" {
		return
	}
	sessionPortAffinity.Store(sessionID, port)
}

func preferredSessionPort(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if port, ok := sessionPortAffinity.Load(sessionID); ok {
		if s, ok := port.(string); ok {
			return s
		}
	}
	return ""
}

func forgetSessionPort(sessionID, port string) {
	if sessionID == "" {
		return
	}
	if port == "" {
		sessionPortAffinity.Delete(sessionID)
		return
	}
	if current, ok := sessionPortAffinity.Load(sessionID); ok && current == port {
		sessionPortAffinity.Delete(sessionID)
	}
}

func forgetSessionsForPort(port string) {
	if port == "" {
		return
	}
	sessionPortAffinity.Range(func(key, value interface{}) bool {
		if value == port {
			sessionPortAffinity.CompareAndDelete(key, value)
		}
		return true
	})
}

// DiscoverOpenCodePortsContext lets each caller cancel its own wait. Shared
// scans have an independent ten-second deadline; timed-out scans are not cached.
func DiscoverOpenCodePortsContext(ctx context.Context) map[string]string {
	if ctx.Err() != nil {
		return nil
	}
	if cached, ok := readCachedPorts(); ok {
		return cached
	}

	const flightKey = "discoverOpenCodePorts"
	result := portFlight.DoChan(flightKey, func() (interface{}, error) {
		scanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cached, ok := readCachedPorts(); ok {
			return cached, nil
		}
		result := (*discoverPortsImpl.Load())(scanCtx)
		if err := scanCtx.Err(); err != nil {
			return nil, err
		}
		portCache.mu.Lock()
		portCache.ports = result
		portCache.updated = time.Now()
		portCache.mu.Unlock()
		return copyMap(result), nil
	})
	select {
	case <-ctx.Done():
		return nil
	case result := <-result:
		if m, ok := result.Val.(map[string]string); ok {
			return copyMap(m)
		}
	}
	return map[string]string{}
}

func readCachedPorts() (map[string]string, bool) {
	portCache.mu.Lock()
	defer portCache.mu.Unlock()
	if time.Since(portCache.updated) < portCacheTTL && portCache.ports != nil {
		return copyMap(portCache.ports), true
	}
	return nil, false
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	for k, v := range m {
		cp[k] = v
	}
	return cp
}

func copyOpenCodeServers(servers []openCodeServer) []openCodeServer {
	return append([]openCodeServer(nil), servers...)
}

func writeCachedPorts(ports map[string]string) {
	portCache.mu.Lock()
	portCache.ports = ports
	portCache.updated = time.Now()
	portCache.mu.Unlock()
}

func normalizePortDirectory(directory string) string {
	clean := filepath.Clean(directory)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		return resolved
	}
	return clean
}

// foldWorktreeToProjectRoot folds a worktree session directory back to
// the main checkout that hosts its (single) OpenCode instance:
//
//	<prefix>/.worktrees/<repo>/<slug>...  ->  <prefix>/<repo>
//
// Worktree sessions run on the project's shared instance (rooted at the
// main checkout, see EnsureProjectOpencode), so their own directory is
// never a key in the port map. Folding to the project root lets the
// directory-keyed liveness/port lookup find the hosting instance.
//
// Any path that doesn't match the worktree layout is returned unchanged.
// Mirrors server.projectRootForDirectory / git.WorktreePathFor / the
// frontend's worktrees.ts helper (kept local to avoid an import cycle).
func foldWorktreeToProjectRoot(directory string) string {
	if directory == "" {
		return directory
	}
	parts := strings.Split(filepath.Clean(directory), "/")
	idx := -1
	for i, p := range parts {
		if p == ".worktrees" {
			idx = i
			break
		}
	}
	// Need <prefix>/.worktrees/<repo>/<slug>, and a non-empty prefix
	// (idx==0 means a relative/root path with no distinguishable prefix).
	if idx <= 0 || len(parts) < idx+3 {
		return directory
	}
	prefix := strings.Join(parts[:idx], "/")
	if prefix == "" {
		return directory
	}
	return prefix + "/" + parts[idx+1]
}

func duplicateOpenCodeServerPortsForDirectory(directory string, servers []openCodeServer) []string {
	key := normalizePortDirectory(directory)
	seen := make(map[string]struct{})
	for _, server := range servers {
		if server.directory != key || server.port == "" {
			continue
		}
		seen[server.port] = struct{}{}
	}
	if len(seen) < 2 {
		return nil
	}
	ports := make([]string, 0, len(seen))
	for port := range seen {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	return ports
}

func discoverDuplicateOpenCodeServerPorts(directory string) []string {
	return duplicateOpenCodeServerPortsForDirectory(directory, discoverOpenCodeServers())
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory, or "" if not found.
// On an exact-match miss it folds a worktree directory back to its project
// root — worktree sessions run on the project's shared instance (rooted at
// the main checkout), so their own directory is never a key in the port map.
// Without the fold, the live per-session SSE stream and the permission-prompt
// fetch resolve to "" and fall back to a fragile by-session HTTP probe,
// dropping permission/question prompts for worktree sessions.
func discoverOpenCodePort(directory string) string {
	return lookupPortWithWorktreeFold(discoverOpenCodePorts(), directory)
}

// lookupPortWithWorktreeFold looks up a directory in the port map, folding a
// worktree directory to its project root on an exact-match miss. Mirrors
// directoryHasLivePort's fold so port resolution and liveness agree.
func lookupPortWithWorktreeFold(ports map[string]string, directory string) string {
	if port := ports[normalizePortDirectory(directory)]; port != "" {
		return port
	}
	if root := foldWorktreeToProjectRoot(directory); root != directory {
		if port := ports[normalizePortDirectory(root)]; port != "" {
			return port
		}
	}
	return MachineServerPort()
}

// discoverOpenCodePortFresh performs an uncached scan for a single
// directory. Misses are deliberately not cached: callers use this
// immediately after launching OpenCode, when a process may not have
// bound its port yet and caching that transient miss would hide the
// process for portCacheTTL. Hits refresh the shared cache so subsequent
// read-heavy callers can reuse the fresh snapshot.
func discoverOpenCodePortFresh(directory string) string {
	ports := (*discoverPortsImpl.Load())(context.Background())
	port := lookupPortWithWorktreeFold(ports, directory)
	if port != "" {
		writeCachedPorts(ports)
	}
	return port
}

// discoveredOpenCodeDirs returns "dir=port" for every currently running
// opencode instance, for diagnostic logging when a directory lookup
// misses. Uses the same seam as the fresh scan so the snapshot matches.
func discoveredOpenCodeDirs() []string {
	ports := (*discoverPortsImpl.Load())(context.Background())
	out := make([]string, 0, len(ports))
	for dir, port := range ports {
		out = append(out, dir+"="+port)
	}
	sort.Strings(out)
	return out
}

// DiscoverOpenCodePort is the exported equivalent of discoverOpenCodePort,
// provided so packages outside the opencode package (e.g. server) can
// resolve the port for a directory without creating an import cycle
// through the Platform adapter interface.
func DiscoverOpenCodePort(directory string) string {
	return discoverOpenCodePort(directory)
}

func DiscoverOpenCodePortContext(ctx context.Context, directory string) string {
	ports := DiscoverOpenCodePortsContext(ctx)
	if ctx.Err() != nil {
		return ""
	}
	return lookupPortWithWorktreeFold(ports, directory)
}

// DiscoverOpenCodePortFresh is the exported equivalent of
// discoverOpenCodePortFresh for launch/wait flows that must not cache a
// transient miss while a just-started OpenCode process is still binding.
func DiscoverOpenCodePortFresh(directory string) string {
	return discoverOpenCodePortFresh(directory)
}

// DiscoverOpenCodePorts returns a fresh snapshot of every running
// OpenCode instance as a directory -> port map. The result is a copy,
// safe for the caller to mutate.
//
// Used by the headless auto-approve watcher (internal/server) to
// enumerate OpenCode processes it should subscribe to. Goes through the
// same cached path as DiscoverOpenCodePort so back-to-back calls do not
// run lsof again within portCacheTTL.
func DiscoverOpenCodePorts() map[string]string {
	return discoverOpenCodePorts()
}

// discoverOpenCodePortCtx is discoverOpenCodePort with Server-Timing instrumentation.
func discoverOpenCodePortCtx(ctx context.Context, directory string) string {
	if cached, ok := readCachedPorts(); ok {
		hit := srvtiming.Begin(ctx, "lsof_hit")
		port := lookupPortWithWorktreeFold(cached, directory)
		hit.End()
		return port
	}
	miss := srvtiming.Begin(ctx, "lsof_miss")
	port := discoverOpenCodePort(directory)
	miss.EndWithDesc("ran fresh lsof scan")
	return port
}

func resolveOpenCodePortForSessionCtx(ctx context.Context, sessionID, directory string) string {
	if port := preferredSessionPort(sessionID); port != "" {
		return port
	}
	port := discoverOpenCodePortCtx(ctx, directory)
	rememberSessionPort(sessionID, port)
	return port
}
