package ocv2

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Service is OpenCode v2's background service (`opencode serve
// --service`) as it registers itself in the state directory. The CLI
// starts it on first use and every later `opencode` attaches to it, so
// it is the one server a machine normally runs.
type Service struct {
	URL     string
	PID     int
	Version string
}

// ErrNoService means no live background service is registered.
var ErrNoService = errors.New("no OpenCode background service is running")

// ServiceStatePath is the registration file the running service writes.
func ServiceStatePath() string {
	return filepath.Join(xdgDir("XDG_STATE_HOME", ".local", "state"), "opencode", "service.json")
}

// ServiceConfigPath holds the password every service start reuses.
func ServiceConfigPath() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "opencode", "service.json")
}

func xdgDir(env string, fallback ...string) string {
	if dir := os.Getenv(env); filepath.IsAbs(dir) {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(append([]string{home}, fallback...)...)
}

// processAlive is a seam for tests.
var processAlive = func(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

// RunningService reads the registered background service. A missing
// file, a dead pid, a non-loopback URL or a v1 version is ErrNoService:
// ocman only ever talks to its own machine's OpenCode over loopback.
func RunningService() (Service, error) {
	b, err := os.ReadFile(ServiceStatePath())
	if errors.Is(err, os.ErrNotExist) {
		return Service{}, ErrNoService
	}
	if err != nil {
		return Service{}, fmt.Errorf("reading OpenCode service registration: %w", err)
	}
	var rec struct {
		URL     string `json:"url"`
		PID     int    `json:"pid"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return Service{}, fmt.Errorf("%w: invalid registration: %w", ErrNoService, err)
	}
	u, err := url.Parse(rec.URL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || (u.Path != "" && u.Path != "/") {
		return Service{}, fmt.Errorf("%w: unsupported service URL %q", ErrNoService, rec.URL)
	}
	if Major(rec.Version) < 2 {
		return Service{}, fmt.Errorf("%w: service version %q is not v2", ErrNoService, rec.Version)
	}
	if rec.PID <= 0 || !processAlive(rec.PID) {
		return Service{}, fmt.Errorf("%w: registered pid %d is not running", ErrNoService, rec.PID)
	}
	return Service{URL: strings.TrimSuffix(rec.URL, "/"), PID: rec.PID, Version: rec.Version}, nil
}

// ServicePassword returns the password the background service runs
// with, or "" when none is configured yet.
func ServicePassword() (string, error) {
	b, err := os.ReadFile(ServiceConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading OpenCode service config: %w", err)
	}
	var cfg struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return "", fmt.Errorf("parsing OpenCode service config: %w", err)
	}
	return cfg.Password, nil
}
