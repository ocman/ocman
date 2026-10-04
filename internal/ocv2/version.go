package ocv2

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// installedTTL bounds how long the `opencode --version` answer is
// reused, so an upgrade or downgrade is picked up without a restart.
const installedTTL = time.Minute

var installed struct {
	mu sync.Mutex
	v2 bool
	at time.Time
}

var installedOverride atomic.Pointer[bool]

// versionCommand is a seam for tests.
var versionCommand = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "opencode", "--version").Output()
}

// InstalledV2 reports whether the opencode binary on PATH is v2 or
// later. v1 and v2 are not installed side by side, so this decides how
// this machine launches and reads OpenCode.
func InstalledV2() bool {
	if v := installedOverride.Load(); v != nil {
		return *v
	}
	installed.mu.Lock()
	defer installed.mu.Unlock()
	if !installed.at.IsZero() && time.Since(installed.at) < installedTTL {
		return installed.v2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := versionCommand(ctx)
	installed.v2 = err == nil && Major(string(out)) >= 2
	installed.at = time.Now()
	return installed.v2
}

// SetInstalledV2 pins InstalledV2 (tests and the -opencode-v2 override).
// It returns a restore func.
func SetInstalledV2(v bool) func() {
	prev := installedOverride.Swap(&v)
	return func() { installedOverride.Store(prev) }
}

// Major parses the major version from "opencode v2.0.22" (v2's
// `--version`), "2.0.22" (its /api/info) or "1.18.32" (v1).
func Major(version string) int {
	fields := strings.Fields(version)
	if len(fields) == 0 {
		return 0
	}
	v := strings.TrimPrefix(fields[len(fields)-1], "v")
	head, _, _ := strings.Cut(v, ".")
	n, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return n
}

// hostTTL bounds how long a server's dialect is trusted: a port can be
// reused by a different process after a restart.
const hostTTL = 30 * time.Second

type hostEntry struct {
	v2 bool
	at time.Time
}

var hosts sync.Map // "host:port" -> hostEntry

// probeRT is the authenticated transport used by IsV2; Wrap records it.
var probeRT atomic.Pointer[http.RoundTripper]

// detect reports whether the server at scheme://host speaks v2, using
// GET /api/info (v2 answers JSON with its version; v1 has no such route).
func detect(ctx context.Context, rt http.RoundTripper, scheme, host string) (bool, error) {
	if v, ok := hosts.Load(host); ok {
		if e := v.(hostEntry); time.Since(e.at) < hostTTL {
			return e.v2, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+host+"/api/info", nil)
	if err != nil {
		return false, err
	}
	resp, err := rt.RoundTrip(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	var info struct {
		Version string `json:"version"`
	}
	v2 := resp.StatusCode == http.StatusOK &&
		strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") &&
		json.NewDecoder(resp.Body).Decode(&info) == nil && Major(info.Version) >= 2
	hosts.Store(host, hostEntry{v2: v2, at: time.Now()})
	return v2, nil
}

// IsV2 reports whether the OpenCode server on loopback port speaks v2.
// Always false on a machine whose installed opencode is v1.
func IsV2(ctx context.Context, port string) bool {
	if !InstalledV2() {
		return false
	}
	rt := probeRT.Load()
	if rt == nil {
		return false
	}
	v2, err := detect(ctx, *rt, "http", "127.0.0.1:"+port)
	return err == nil && v2
}

// ForgetHost drops the cached dialect for a host (tests, relaunches).
func ForgetHost(host string) { hosts.Delete(host) }
