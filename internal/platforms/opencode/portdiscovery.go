package opencode

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"runtime"
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

// portFlight coalesces concurrent cold-cache callers into a single
// underlying lsof invocation.
var portFlight singleflight.Group

// portCacheTTL bounds how long a discovered port map is reused
// before the next request triggers a fresh lsof scan.
const portCacheTTL = 10 * time.Second

// discoverPortsImpl is the seam used so tests can swap out the expensive
// lsof execution. Stored as an atomic pointer so reads and writes are
// race-free without a mutex.
var discoverPortsImpl atomic.Pointer[func() map[string]string]

func init() {
	fn := func() map[string]string { return discoverOpenCodePortsUncached() }
	discoverPortsImpl.Store(&fn)
}

// setDiscoverPortsImplForTests installs fn as the seam and returns a
// restore func that re-installs the previous value.
func setDiscoverPortsImplForTests(fn func() map[string]string) func() {
	prev := discoverPortsImpl.Swap(&fn)
	return func() { discoverPortsImpl.Store(prev) }
}

// resetPortCacheForTests clears the cache so each test starts with a cold path.
func resetPortCacheForTests() {
	portCache.mu.Lock()
	portCache.ports = nil
	portCache.updated = time.Time{}
	portCache.mu.Unlock()
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

// pidPort is a (pid, port) pair extracted from `lsof -iTCP -sTCP:LISTEN`.
type pidPort struct {
	pid  string
	port string
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

// discoverOpenCodePortsUncached performs the actual lsof-based discovery.
// Two-phase: enumerate listening opencode PIDs, then fan-out to resolve cwds.
func discoverOpenCodePortsUncached() map[string]string {
	result := make(map[string]string)

	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return result
	}

	candidates := parseOpenCodeListeners(string(out))
	if len(candidates) == 0 {
		return result
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

	for r := range results {
		result[r.dir] = r.port
	}

	return result
}

// discoverOpenCodePort finds the HTTP port of a running OpenCode instance
// whose working directory matches the given directory, or "" if not found.
func discoverOpenCodePort(directory string) string {
	return discoverOpenCodePorts()[directory]
}

// DiscoverOpenCodePort is the exported equivalent of discoverOpenCodePort,
// provided so packages outside the opencode package (e.g. server) can
// resolve the port for a directory without creating an import cycle
// through the Platform adapter interface.
func DiscoverOpenCodePort(directory string) string {
	return discoverOpenCodePort(directory)
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
		port := cached[directory]
		hit.End()
		return port
	}
	miss := srvtiming.Begin(ctx, "lsof_miss")
	port := discoverOpenCodePort(directory)
	miss.EndWithDesc("ran fresh lsof scan")
	return port
}
