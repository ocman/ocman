package ocruntime

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

// NativeConnectionConfig names every writable OpenCode account location.
// ConfigDir is the directory containing opencode.json, not XDG_CONFIG_HOME.
// Executable may be an absolute CLI path or a name resolved by the safe PATH.
// Nothing here is inferred from process-level OPENCODE_* overrides.
type NativeConnectionConfig struct {
	DBPath, ConfigDir, DataDir, StateDir, CacheDir, RuntimeDir string
	Executable                                                 string
}

func (c NativeConnectionConfig) Validate() error {
	paths := []string{c.DBPath, c.ConfigDir, c.DataDir, c.StateDir, c.CacheDir, c.RuntimeDir}
	for _, path := range paths {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\n\r") {
			return fmt.Errorf("OpenCode connection requires explicit absolute storage paths")
		}
	}
	if c.Executable == "" || strings.ContainsAny(c.Executable, "\x00\n\r") {
		return fmt.Errorf("OpenCode connection requires an explicit executable")
	}
	return nil
}

// NewNativeConnectionRuntime never uses the registered shared background
// service, the process database override or process provider credentials.
func NewNativeConnectionRuntime(c NativeConnectionConfig, auth ocapi.Auth) (*NativeRuntime, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if !auth.Enabled() {
		return nil, fmt.Errorf("OpenCode connection requires private server authentication")
	}
	r := NewNativeRuntimeWithAuth(auth)
	r.connection = &c
	return r, nil
}

// ForInspection keeps selected native storage but uses a fresh process/runtime
// directory. Legacy runtimes cannot bootstrap named-account inspections.
func (r *NativeRuntime) ForInspection(directory string) (Runtime, error) {
	if r.connection == nil || !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("named OpenCode runtime required for inspection")
	}
	copy := *r
	config := *r.connection
	config.RuntimeDir = directory
	copy.connection = &config
	return &copy, nil
}

func (c NativeConnectionConfig) launchEnvironment(auth ocapi.Auth, port int) (map[string]string, string, error) {
	for _, dir := range []string{filepath.Dir(c.DBPath), c.ConfigDir, c.DataDir, c.StateDir, c.CacheDir, c.RuntimeDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, "", fmt.Errorf("prepare OpenCode connection storage: %w", err)
		}
	}
	// An allowlist is intentional. Provider API keys, cloud credential paths,
	// OPENCODE_CONFIG_CONTENT/URL/SERVER and shell startup overrides must not
	// silently turn this account into the parent process's account. HOME stays
	// real for tools and project paths; no global environment is mutated.
	env := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USER", "LOGNAME", "SHELL", "TMPDIR", "LANG", "LC_ALL", "LC_CTYPE", "TERM", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	env["OPENCODE_DB"] = c.DBPath
	env["OPENCODE_CONFIG_DIR"] = c.ConfigDir
	env["XDG_CONFIG_HOME"] = filepath.Join(c.StateDir, "config")
	env["XDG_DATA_HOME"] = c.DataDir
	env["XDG_STATE_HOME"] = c.StateDir
	env["XDG_CACHE_HOME"] = c.CacheDir
	env["XDG_RUNTIME_DIR"] = c.RuntimeDir
	env["NODE_NO_WARNINGS"] = "1"
	auth.AddServerEnv(env)
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	// tmux's login shell may add credentials back. Clean its environment at
	// the final exec boundary too, passing values via expansion rather than
	// embedding passwords in the command text.
	command := "exec /usr/bin/env -i"
	for _, key := range keys {
		command += " " + key + "=\"$" + key + "\""
	}
	command += " " + shellLiteral(c.Executable) + fmt.Sprintf(" serve --port %d", port)
	return env, command, nil
}

func shellLiteral(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
