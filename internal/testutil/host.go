package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// isolatedHostEnv marks an environment IsolateHost already isolated.
// IsolatedEnv keeps it, as an OCMAN_TEST_ variable.
const isolatedHostEnv = "OCMAN_TEST_ISOLATED_HOME"

// IsolateHost gives a test binary a private host for its whole run, so
// tests that build real servers, hosts or native runtimes never reach the
// user's tmux servers (including ocman-agents), OpenCode state or
// database. Call it first in TestMain and run the returned cleanup after
// m.Run:
//
//   - HOME and TMUX_TMPDIR point at a temporary directory, so every tmux
//     socket (`-L` or default) is new;
//   - XDG_*, OPENCODE_* and TMUX* are unset, so user directories resolve
//     under that HOME and nothing names a live server or database;
//   - GOCACHE, GOMODCACHE, GOPATH and GOENV keep their real values, so
//     tests that run `go build` do not start cold or download modules.
//
// The cleanup kills tmux servers a test left in the directory, then
// removes it. A test binary re-executed by an isolated one (as a fake CLI
// or an ocman child) keeps the environment its parent chose.
func IsolateHost() func() {
	if os.Getenv(isolatedHostEnv) != "" {
		return func() {}
	}
	goEnv := realGoEnv()
	// HOME nests provider sockets (~/.local/share/ocman/p/<id>/s), so keep
	// it as short as a real one.
	home, err := os.MkdirTemp(shortTempRoot(os.TempDir()), "h-")
	if err != nil {
		panic(fmt.Errorf("testutil: isolating host: %w", err))
	}
	tmuxDir := filepath.Join(home, ".tmux")
	if err := os.Mkdir(tmuxDir, 0o700); err != nil {
		panic(fmt.Errorf("testutil: isolating host: %w", err))
	}
	// An agent shell inherits TMUX* and OPENCODE_* from ocman's managed
	// OpenCode: TMUX names the live ocman-agents socket, and OPENCODE_DB
	// derives the live machine server's tmux session name. XDG_* would
	// keep pointing into the real home.
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		if key == "TMUX" || strings.HasPrefix(key, "TMUX_") || strings.HasPrefix(key, "OPENCODE_") || strings.HasPrefix(key, "XDG_") {
			_ = os.Unsetenv(key)
		}
	}
	_ = os.Setenv("HOME", home)
	_ = os.Setenv("TMUX_TMPDIR", tmuxDir)
	_ = os.Setenv(isolatedHostEnv, home)
	for key, value := range goEnv {
		_ = os.Setenv(key, value)
	}
	return func() {
		sockets, _ := filepath.Glob(filepath.Join(tmuxDir, "tmux-*", "*"))
		for _, socket := range sockets {
			_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		}
		_ = os.RemoveAll(home)
	}
}

// realGoEnv resolves the Go directories that default from HOME, before
// HOME moves. Without a go command nothing builds, so nothing is pinned.
func realGoEnv() map[string]string {
	keys := []string{"GOCACHE", "GOMODCACHE", "GOPATH", "GOENV"}
	out, err := exec.Command("go", append([]string{"env"}, keys...)...).Output()
	if err != nil {
		return nil
	}
	env := map[string]string{}
	for i, value := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		if i < len(keys) && value != "" {
			env[keys[i]] = value
		}
	}
	return env
}
