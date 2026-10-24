package opencode

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// Discovery is one adapter's view of the OpenCode instances its Host
// runs: the cached directory -> port map, the session -> port affinity,
// the v2 machine server and the managed inventory it reads. Each Adapter
// owns one, so a backend reload (a new adapter) starts from nothing and
// two servers in one process never share routing state.
type Discovery struct {
	// portCache holds cached port discovery results with a single mutex
	// for simplicity; port discovery is infrequent enough that read/write
	// contention is not a concern.
	portCache struct {
		mu               sync.Mutex
		ports            map[string]string
		lastSuccessful   map[string]string
		lastSuccessfulAt time.Time
		updated          time.Time
		generation       uint64
	}
	serverCache struct {
		mu      sync.Mutex
		servers []openCodeServer
		updated time.Time
	}
	sessionPortAffinity sync.Map // sessionID -> port

	// portFlight coalesces concurrent cold-cache callers into a single
	// underlying managed-inventory read.
	portFlight singleflight.Group
	// serverFlight coalesces concurrent cold-cache callers that need the
	// full server list rather than the directory -> port map.
	serverFlight singleflight.Group

	// machineServer is the port of the OpenCode v2 server that serves
	// every directory on this machine ("" on v1 machines).
	machineServer     atomic.Value // string
	managedPortSource atomic.Pointer[func(context.Context) map[string]string]
}

// NewDiscovery returns an empty discovery with no inventory source.
func NewDiscovery() *Discovery { return &Discovery{} }

// portCacheTTL bounds how long a discovered port map is reused
// before the next request refreshes the managed inventory.
const portCacheTTL = 10 * time.Second

// discoverPortsOverride and discoverServersOverride are test seams that
// replace every Discovery's inventory read. They hold no state; nil in
// production.
var discoverPortsOverride atomic.Pointer[func(context.Context) map[string]string]
var discoverServersOverride atomic.Pointer[func() []openCodeServer]

func (d *Discovery) discoverPortsImpl(ctx context.Context) map[string]string {
	if fn := discoverPortsOverride.Load(); fn != nil {
		return (*fn)(ctx)
	}
	return d.discoverOpenCodePortsUncachedContext(ctx)
}

func (d *Discovery) discoverServersImpl() []openCodeServer {
	if fn := discoverServersOverride.Load(); fn != nil {
		return (*fn)()
	}
	return d.discoverOpenCodeServersUncached()
}

// resetPortCache clears the shared port-discovery caches.
func (d *Discovery) resetPortCache() {
	d.portCache.mu.Lock()
	d.portCache.ports = nil
	d.portCache.generation++
	d.portCache.updated = time.Time{}
	d.portCache.mu.Unlock()

	d.serverCache.mu.Lock()
	d.serverCache.servers = nil
	d.serverCache.updated = time.Time{}
	d.serverCache.mu.Unlock()
}

// InvalidateOpenCodePortCache clears cached OpenCode port-discovery data.
// Callers that have just launched or restarted an OpenCode process should
// invalidate so the next lookup reads the managed inventory instead of reusing
// a recently cached "not running yet" result.
func (d *Discovery) InvalidateOpenCodePortCache() {
	d.resetPortCache()
}

func (d *Discovery) rememberSessionPort(sessionID, port string) {
	if sessionID == "" || port == "" {
		return
	}
	d.sessionPortAffinity.Store(sessionID, port)
}

func (d *Discovery) preferredSessionPort(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	if port, ok := d.sessionPortAffinity.Load(sessionID); ok {
		if s, ok := port.(string); ok {
			return s
		}
	}
	return ""
}

func (d *Discovery) forgetSessionPort(sessionID, port string) {
	if sessionID == "" {
		return
	}
	if port == "" {
		d.sessionPortAffinity.Delete(sessionID)
		return
	}
	if current, ok := d.sessionPortAffinity.Load(sessionID); ok && current == port {
		d.sessionPortAffinity.Delete(sessionID)
	}
}

func (d *Discovery) forgetSessionsForPort(port string) {
	if port == "" {
		return
	}
	d.sessionPortAffinity.Range(func(key, value interface{}) bool {
		if value == port {
			d.sessionPortAffinity.CompareAndDelete(key, value)
		}
		return true
	})
}

// DiscoverOpenCodePortsContext lets each caller cancel its own wait. Shared
// scans have an independent ten-second deadline; timed-out scans are not cached.
// A nil map signals failure; a non-nil empty map signals successful emptiness.
func (d *Discovery) DiscoverOpenCodePortsContext(ctx context.Context) map[string]string {
	if d == nil || ctx.Err() != nil {
		return nil
	}
	if cached, ok := d.readCachedPorts(); ok {
		return cached
	}

	d.portCache.mu.Lock()
	generation := d.portCache.generation
	d.portCache.mu.Unlock()
	flightKey := "discoverOpenCodePorts:" + strconv.FormatUint(generation, 10)
	result := d.portFlight.DoChan(flightKey, func() (interface{}, error) {
		scanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cached, ok := d.readCachedPorts(); ok {
			return cached, nil
		}
		result := d.discoverPortsImpl(scanCtx)
		if err := scanCtx.Err(); err != nil {
			return nil, err
		}
		if result == nil {
			return nil, errors.New("OpenCode port discovery failed")
		}
		d.portCache.mu.Lock()
		if d.portCache.generation != generation {
			d.portCache.mu.Unlock()
			return copyMap(result), nil
		}
		d.portCache.ports = result
		d.portCache.lastSuccessful = copyMap(result)
		d.portCache.updated = time.Now()
		d.portCache.lastSuccessfulAt = d.portCache.updated
		d.portCache.mu.Unlock()
		return copyMap(result), nil
	})
	select {
	case <-ctx.Done():
		return nil
	case result := <-result:
		if result.Err != nil {
			return nil
		}
		if m, ok := result.Val.(map[string]string); ok {
			return copyMap(m)
		}
	}
	return map[string]string{}
}

func (d *Discovery) readCachedPorts() (map[string]string, bool) {
	d.portCache.mu.Lock()
	defer d.portCache.mu.Unlock()
	if time.Since(d.portCache.updated) < portCacheTTL && d.portCache.ports != nil {
		return copyMap(d.portCache.ports), true
	}
	return nil, false
}

func copyMap(m map[string]string) map[string]string {
	cp := make(map[string]string, len(m))
	maps.Copy(cp, m)
	return cp
}

func (d *Discovery) writeCachedPorts(ports map[string]string) {
	d.portCache.mu.Lock()
	d.portCache.ports = ports
	d.portCache.lastSuccessful = copyMap(ports)
	d.portCache.updated = time.Now()
	d.portCache.lastSuccessfulAt = d.portCache.updated
	d.portCache.mu.Unlock()
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
	return slices.Sorted(maps.Keys(seen))
}

func (d *Discovery) discoverDuplicateOpenCodeServerPorts(directory string) []string {
	return duplicateOpenCodeServerPortsForDirectory(directory, d.discoverOpenCodeServers())
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory, or "" if not found.
// On an exact-match miss it folds a worktree directory back to its project
// root — worktree sessions run on the project's shared instance (rooted at
// the main checkout), so their own directory is never a key in the port map.
// Without the fold, the live per-session SSE stream and the permission-prompt
// fetch resolve to "" and fall back to a fragile by-session HTTP probe,
// dropping permission/question prompts for worktree sessions.
func (d *Discovery) discoverOpenCodePort(directory string) string {
	return d.lookupPortWithWorktreeFold(d.discoverOpenCodePorts(), directory)
}

// lookupPortWithWorktreeFold looks up a directory in the port map, folding a
// worktree directory to its project root on an exact-match miss. Mirrors
// directoryHasLivePort's fold so port resolution and liveness agree.
func (d *Discovery) lookupPortWithWorktreeFold(ports map[string]string, directory string) string {
	if port := ports[normalizePortDirectory(directory)]; port != "" {
		return port
	}
	if root := foldWorktreeToProjectRoot(directory); root != directory {
		if port := ports[normalizePortDirectory(root)]; port != "" {
			return port
		}
	}
	return d.MachineServerPort()
}

// discoverOpenCodePortFresh performs an uncached scan for a single
// directory. Misses are deliberately not cached: callers use this
// immediately after launching OpenCode, when a process may not have
// bound its port yet and caching that transient miss would hide the
// process for portCacheTTL. Hits refresh the shared cache so subsequent
// read-heavy callers can reuse the fresh snapshot.
func (d *Discovery) discoverOpenCodePortFresh(directory string) string {
	ports := d.discoverPortsImpl(context.Background())
	port := d.lookupPortWithWorktreeFold(ports, directory)
	if port != "" {
		d.writeCachedPorts(ports)
	}
	return port
}

// discoveredOpenCodeDirs returns "dir=port" for every currently running
// opencode instance, for diagnostic logging when a directory lookup
// misses. Uses the same seam as the fresh scan so the snapshot matches.
func (d *Discovery) discoveredOpenCodeDirs() []string {
	ports := d.discoverPortsImpl(context.Background())
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
func (d *Discovery) DiscoverOpenCodePort(directory string) string {
	return d.discoverOpenCodePort(directory)
}

func (d *Discovery) DiscoverOpenCodePortContext(ctx context.Context, directory string) string {
	if d == nil {
		return ""
	}
	ports := d.DiscoverOpenCodePortsContext(ctx)
	if ctx.Err() != nil {
		return ""
	}
	return d.lookupPortWithWorktreeFold(ports, directory)
}

// DiscoverOpenCodePorts returns a fresh snapshot of every running
// OpenCode instance as a directory -> port map. The result is a copy,
// safe for the caller to mutate.
//
// Used by the headless auto-approve watcher (internal/server) to
// enumerate OpenCode processes it should subscribe to. Goes through the
// same cached path as DiscoverOpenCodePort so back-to-back calls do not
// refresh the managed inventory again within portCacheTTL.
func (d *Discovery) DiscoverOpenCodePorts() map[string]string {
	return d.discoverOpenCodePorts()
}

// discoverOpenCodePortCtx is discoverOpenCodePort with Server-Timing instrumentation.
func (d *Discovery) discoverOpenCodePortCtx(ctx context.Context, directory string) string {
	if cached, ok := d.readCachedPorts(); ok {
		hit := srvtiming.Begin(ctx, "runtime_ports_hit")
		port := d.lookupPortWithWorktreeFold(cached, directory)
		hit.End()
		return port
	}
	miss := srvtiming.Begin(ctx, "runtime_ports_miss")
	port := d.discoverOpenCodePort(directory)
	miss.EndWithDesc("read managed runtime inventory")
	return port
}
