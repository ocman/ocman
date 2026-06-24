package opencode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// rePortSuffix matches a port number at the end of a string (e.g. ":4096").
var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

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
var discoverPortsImpl atomic.Pointer[func() map[string]string]
var discoverServersImpl atomic.Pointer[func() []openCodeServer]

func init() {
	serverFn := func() []openCodeServer { return discoverOpenCodeServersUncached() }
	discoverServersImpl.Store(&serverFn)

	fn := func() map[string]string { return discoverOpenCodePortsUncached() }
	discoverPortsImpl.Store(&fn)
}

// setDiscoverPortsImplForTests installs fn as the seam and returns a
// restore func that re-installs the previous value.
func setDiscoverPortsImplForTests(fn func() map[string]string) func() {
	prev := discoverPortsImpl.Swap(&fn)
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

// discoverOpenCodePorts returns a map of directory -> port for all running
// OpenCode instances. Results are cached for portCacheTTL.
func discoverOpenCodePorts() map[string]string {
	if cached, ok := readCachedPorts(); ok {
		return cached
	}

	const flightKey = "discoverOpenCodePorts"
	v, _, _ := portFlight.Do(flightKey, func() (interface{}, error) {
		if cached, ok := readCachedPorts(); ok {
			return cached, nil
		}
		result := (*discoverPortsImpl.Load())()
		portCache.mu.Lock()
		portCache.ports = result
		portCache.updated = time.Now()
		portCache.mu.Unlock()
		return copyMap(result), nil
	})
	if m, ok := v.(map[string]string); ok {
		return m
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

// pidPort is a (pid, port) pair extracted from `lsof -iTCP -sTCP:LISTEN`.
type pidPort struct {
	pid  string
	port string
}

type openCodeServer struct {
	directory string
	port      string
}

// parseOpenCodeListeners extracts (pid, port) pairs for OpenCode
// processes from the output of `lsof -iTCP -sTCP:LISTEN -P -n`.
//
// Factored out of discoverOpenCodePortsUncached so the matching rules
// (including lsof's 9-char COMMAND truncation) can be unit-tested
// without spawning a real lsof.
func parseOpenCodeListeners(lsofOut string) []pidPort {
	var candidates []pidPort
	for _, line := range strings.Split(lsofOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		// lsof's COMMAND column is truncated to 9 characters by
		// default, so the OpenCode v2+ single-file binary
		// "opencode.exe" appears as "opencode.". Accept any command
		// that starts with "opencode" so we keep working across
		// renames and the lsof truncation boundary.
		if !strings.HasPrefix(fields[0], "opencode") {
			continue
		}
		pid := fields[1]
		if _, err := strconv.Atoi(pid); err != nil {
			log.WithField("pid", pid).Warn("skipping non-numeric PID in lsof output")
			continue
		}
		name := fields[len(fields)-2]
		m := rePortSuffix.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		candidates = append(candidates, pidPort{pid: pid, port: m[1]})
	}
	return candidates
}

// pidCwd returns the working directory of the process with the given PID.
//
// On Linux it reads /proc/<pid>/cwd via a single readlink(2) syscall,
// avoiding a second lsof fork entirely. On other platforms (and as a
// fallback when /proc is unavailable) it shells out to
// `lsof -a -p <pid> -d cwd -F n`.
func pidCwd(pid string) (string, bool) {
	if runtime.GOOS == "linux" {
		if dir, err := os.Readlink(fmt.Sprintf("/proc/%s/cwd", pid)); err == nil {
			return dir, true
		}
		// /proc unavailable (container without procfs, etc.) — fall through.
	}
	cwdOut, err := exec.Command("lsof", "-a", "-p", pid, "-d", "cwd", "-F", "n").Output()
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(cwdOut), "\n") {
		if strings.HasPrefix(line, "n/") {
			return line[1:], true
		}
	}
	return "", false
}

// discoverOpenCodeServersUncached performs the actual lsof-based discovery.
// Two-phase: enumerate listening opencode PIDs, then fan-out to resolve cwds.
func discoverOpenCodeServersUncached() []openCodeServer {
	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return nil
	}

	candidates := parseOpenCodeListeners(string(out))
	if len(candidates) == 0 {
		return nil
	}

	const maxWorkers = 16
	workers := maxWorkers
	if len(candidates) < workers {
		workers = len(candidates)
	}

	type cwdResult struct {
		dir  string
		port string
	}
	jobs := make(chan pidPort, len(candidates))
	results := make(chan cwdResult, len(candidates))

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				if dir, ok := pidCwd(c.pid); ok {
					results <- cwdResult{dir: dir, port: c.port}
				}
			}
		}()
	}

	for _, c := range candidates {
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	close(results)

	servers := make([]openCodeServer, 0, len(candidates))
	for r := range results {
		servers = append(servers, openCodeServer{
			directory: normalizePortDirectory(r.dir),
			port:      r.port,
		})
	}

	return servers
}

func discoverOpenCodeServers() []openCodeServer {
	if cached, ok := readCachedServers(); ok {
		return cached
	}

	const flightKey = "discoverOpenCodeServers"
	v, _, _ := serverFlight.Do(flightKey, func() (interface{}, error) {
		if cached, ok := readCachedServers(); ok {
			return cached, nil
		}
		result := (*discoverServersImpl.Load())()
		serverCache.mu.Lock()
		serverCache.servers = copyOpenCodeServers(result)
		serverCache.updated = time.Now()
		serverCache.mu.Unlock()
		return copyOpenCodeServers(result), nil
	})
	if servers, ok := v.([]openCodeServer); ok {
		return servers
	}
	return nil
}

func readCachedServers() ([]openCodeServer, bool) {
	serverCache.mu.Lock()
	defer serverCache.mu.Unlock()
	if time.Since(serverCache.updated) < portCacheTTL && serverCache.servers != nil {
		return copyOpenCodeServers(serverCache.servers), true
	}
	return nil, false
}

// discoverOpenCodePortsUncached returns one directory -> port entry per
// running OpenCode directory. If multiple servers share a directory, the
// selected port remains intentionally unspecified, matching the previous map
// assignment behavior.
func discoverOpenCodePortsUncached() map[string]string {
	result := make(map[string]string)
	for _, server := range discoverOpenCodeServersUncached() {
		result[server.directory] = server.port
	}
	return result
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
func discoverOpenCodePort(directory string) string {
	return discoverOpenCodePorts()[normalizePortDirectory(directory)]
}

// discoverOpenCodePortFresh performs an uncached scan for a single
// directory. Misses are deliberately not cached: callers use this
// immediately after launching OpenCode, when a process may not have
// bound its port yet and caching that transient miss would hide the
// process for portCacheTTL. Hits refresh the shared cache so subsequent
// read-heavy callers can reuse the fresh snapshot.
func discoverOpenCodePortFresh(directory string) string {
	ports := (*discoverPortsImpl.Load())()
	port := ports[normalizePortDirectory(directory)]
	if port != "" {
		writeCachedPorts(ports)
	}
	return port
}

// DiscoverOpenCodePort is the exported equivalent of discoverOpenCodePort,
// provided so packages outside the opencode package (e.g. server) can
// resolve the port for a directory without creating an import cycle
// through the Platform adapter interface.
func DiscoverOpenCodePort(directory string) string {
	return discoverOpenCodePort(directory)
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
	key := normalizePortDirectory(directory)
	if cached, ok := readCachedPorts(); ok {
		hit := srvtiming.Begin(ctx, "lsof_hit")
		port := cached[key]
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
