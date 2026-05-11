package opencode

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/srvtiming"
)

// rePortSuffix matches a port number at the end of a string (e.g. ":4096").
var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

// portCache holds cached port discovery results. Reads are guarded
// by an RWMutex so cache hits are non-blocking even under heavy
// concurrency; writes happen briefly after the singleflight-protected
// lsof scan completes.
var portCache struct {
	mu      sync.RWMutex
	ports   map[string]string
	updated time.Time
}

// portFlight coalesces concurrent cold-cache callers into a single
// underlying lsof invocation.
var portFlight singleflight.Group

// portCacheTTL bounds how long a discovered port map is reused
// before the next request triggers a fresh lsof scan.
const portCacheTTL = 10 * time.Second

// discoverPortsImpl is the indirection used so tests can swap out the
// expensive lsof execution.
var (
	discoverPortsImplMu sync.RWMutex
	discoverPortsImpl   = discoverOpenCodePortsUncached
)

func getDiscoverPortsImpl() func() map[string]string {
	discoverPortsImplMu.RLock()
	defer discoverPortsImplMu.RUnlock()
	return discoverPortsImpl
}

// setDiscoverPortsImplForTests installs fn as the seam and returns a
// restore func that re-installs the previous value.
func setDiscoverPortsImplForTests(fn func() map[string]string) func() {
	discoverPortsImplMu.Lock()
	prev := discoverPortsImpl
	discoverPortsImpl = fn
	discoverPortsImplMu.Unlock()
	return func() {
		discoverPortsImplMu.Lock()
		discoverPortsImpl = prev
		discoverPortsImplMu.Unlock()
	}
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
		result := getDiscoverPortsImpl()()
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
	portCache.mu.RLock()
	defer portCache.mu.RUnlock()
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

// discoverOpenCodePortsUncached performs the actual lsof-based discovery.
// Two-phase: enumerate listening opencode PIDs, then fan-out to resolve cwds.
func discoverOpenCodePortsUncached() map[string]string {
	result := make(map[string]string)

	out, err := exec.Command("lsof", "-iTCP", "-sTCP:LISTEN", "-P", "-n").Output()
	if err != nil {
		return result
	}

	type pidPort struct {
		pid  string
		port string
	}
	var candidates []pidPort
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 {
			continue
		}
		if fields[0] != "opencode" {
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
				cwdOut, err := exec.Command("lsof", "-a", "-p", c.pid, "-d", "cwd", "-F", "n").Output()
				if err != nil {
					continue
				}
				for _, line := range strings.Split(string(cwdOut), "\n") {
					if strings.HasPrefix(line, "n/") {
						results <- cwdResult{dir: line[1:], port: c.port}
						break
					}
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
