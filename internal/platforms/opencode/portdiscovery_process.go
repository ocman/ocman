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
	"time"

	"github.com/NoUseFreak/ocman/internal/ocv2"
	log "github.com/sirupsen/logrus"
)

var rePortSuffix = regexp.MustCompile(`:(\d+)$`)

type pidPort struct {
	pid  string
	port string
}

type openCodeServer struct {
	directory string
	port      string
}

// parseOpenCodeListeners accepts lsof's nine-character command truncation.
func parseOpenCodeListeners(lsofOut string) []pidPort {
	var candidates []pidPort
	for _, line := range strings.Split(lsofOut, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 9 || !strings.HasPrefix(fields[0], "opencode") {
			continue
		}
		pid := fields[1]
		if _, err := strconv.Atoi(pid); err != nil {
			log.WithField("pid", pid).Warn("skipping non-numeric PID in lsof output")
			continue
		}
		m := rePortSuffix.FindStringSubmatch(fields[len(fields)-2])
		if m != nil {
			candidates = append(candidates, pidPort{pid: pid, port: m[1]})
		}
	}
	return candidates
}

func lsofOutput(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "lsof", args...)
	cmd.WaitDelay = 100 * time.Millisecond
	return cmd.Output()
}

func pidCwd(pid string) (string, bool) {
	return pidCwdContext(context.Background(), pid)
}

// Linux uses /proc; other hosts and unavailable procfs use a scoped lsof read.
func pidCwdContext(ctx context.Context, pid string) (string, bool) {
	if ctx.Err() != nil {
		return "", false
	}
	if runtime.GOOS == "linux" {
		if dir, err := os.Readlink(fmt.Sprintf("/proc/%s/cwd", pid)); err == nil {
			return dir, true
		}
	}
	cwdOut, err := lsofOutput(ctx, "-a", "-p", pid, "-d", "cwd", "-F", "n")
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

var lsofWarnOnce sync.Once

func discoverOpenCodeServersUncached() []openCodeServer {
	return discoverOpenCodeServersUncachedContext(context.Background())
}

// Enumerate listeners, then resolve their directories with bounded fan-out.
func discoverOpenCodeServersUncachedContext(ctx context.Context) []openCodeServer {
	if ocv2.InstalledV2() {
		return machineServers()
	}
	out, err := lsofOutput(ctx, "-iTCP", "-sTCP:LISTEN", "-P", "-n")
	if err != nil {
		if ctx.Err() == nil {
			lsofWarnOnce.Do(func() {
				log.WithError(err).Warn("lsof failed; external OpenCode instance discovery skipped")
			})
		}
		return nil
	}
	candidates := parseOpenCodeListeners(string(out))
	if len(candidates) == 0 {
		return nil
	}
	workers := min(16, len(candidates))
	type cwdResult struct {
		dir  string
		port string
	}
	jobs := make(chan pidPort, len(candidates))
	results := make(chan cwdResult, len(candidates))
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for c := range jobs {
				if ctx.Err() != nil {
					return
				}
				if dir, ok := pidCwdContext(ctx, c.pid); ok {
					results <- cwdResult{dir: dir, port: c.port}
				}
			}
		})
	}
	for _, c := range candidates {
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	close(results)
	servers := make([]openCodeServer, 0, len(candidates))
	for r := range results {
		servers = append(servers, openCodeServer{directory: normalizePortDirectory(r.dir), port: r.port})
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

// Multiple servers for one directory retain the existing unspecified choice.
func discoverOpenCodePortsUncachedContext(ctx context.Context) map[string]string {
	result := make(map[string]string)
	for _, server := range discoverOpenCodeServersUncachedContext(ctx) {
		result[server.directory] = server.port
	}
	return result
}
