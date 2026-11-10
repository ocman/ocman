package ocv2

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

// versionCheckInterval is how often the installed version is re-read to
// warn about a downgrade below v2 while ocman runs; a variable for tests.
var versionCheckInterval = time.Minute

// versionCommand is a seam for tests.
var versionCommand = func(ctx context.Context) ([]byte, error) {
	return exec.CommandContext(ctx, "opencode", "--version").Output()
}

// InstalledMajor runs `opencode --version` and returns its major version.
// ok is false when the binary is missing, fails or prints no version.
func InstalledMajor() (major int, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := versionCommand(ctx)
	if err != nil {
		return 0, false
	}
	major = Major(string(out))
	return major, major > 0
}

// WatchInstalledVersion re-reads the installed version until ctx ends and
// warns once when it drops below v2, which ocman no longer supports. A
// failed read (slow or missing binary) is ignored.
func WatchInstalledVersion(ctx context.Context) {
	defer module.StopSchedule(ctx, "opencode-version")
	tick := time.NewTicker(versionCheckInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		downgraded := false
		module.Scheduled(ctx, "opencode-version", versionCheckInterval, func(ctx context.Context) {
			if major, ok := InstalledMajor(); ok && major < 2 {
				module.ReportError(ctx, errors.New("installed OpenCode version is no longer supported"))
				log.WithField("installedMajor", major).Error("OpenCode was downgraded below v2, which ocman does not support; upgrade OpenCode and restart ocman")
				downgraded = true
			}
		})
		if downgraded {
			return
		}
	}
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

// ResetHostCache forgets every server's dialect, so a backend reload
// re-probes instead of trusting a previous backend's answers.
func ResetHostCache() { hosts.Clear() }

// detect reports whether the server at scheme://host speaks v2, using
// GET /api/info, which answers JSON with its version. Startup refuses an
// installed v1, so in production every managed server is v2; a server
// without the route (a test fake serving the v1-shaped routes directly)
// gets requests unchanged.
func detect(ctx context.Context, rt http.RoundTripper, scheme, host string) (bool, error) {
	return detectWithCache(ctx, rt, scheme, host, &hosts)
}

func detectWithCache(ctx context.Context, rt http.RoundTripper, scheme, host string, cache *sync.Map) (bool, error) {
	if v, ok := cache.Load(host); ok {
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
	cache.Store(host, hostEntry{v2: v2, at: time.Now()})
	return v2, nil
}

// ForgetHost drops the cached dialect for a host (tests, relaunches).
func ForgetHost(host string) { hosts.Delete(host) }
