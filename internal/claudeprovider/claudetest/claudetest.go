// Package claudetest builds the fake claude executable that replays the
// spike's stream-json fixtures. Tests use it instead of the real CLI.
package claudetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// Version is the CLI version the fixtures were recorded with.
const Version = "2.1.220"

func root() string {
	_, source, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(source), "..", "..", "..")
}

// InstallFakeTmux installs the provider test shim and cleans up its supervisors.
func InstallFakeTmux(t testing.TB, bin, pids string) {
	t.Helper()
	script, err := os.ReadFile(filepath.Join(root(), "internal", "hostsvc", "local", "testdata", "provider-tmux"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{bin, pids} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(bin, "tmux"), script, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = exec.Command(filepath.Join(bin, "tmux"), "-L", "x", "-f", "/dev/null", "has-session", "-t", "warm").Run()
	t.Cleanup(func() {
		paths, _ := filepath.Glob(filepath.Join(pids, "*.pid"))
		for _, path := range paths {
			data, _ := os.ReadFile(path)
			if pid, _ := strconv.Atoi(string(data)); pid > 0 {
				if p, err := os.FindProcess(pid); err == nil {
					_ = p.Kill()
				}
			}
		}
	})
}

// Binary builds a caller-owned fake as "claude", so it can be moved onto PATH.
// Warm it before returning: macOS first-exec latency must not consume the
// production version-probe or readiness deadline.
func Binary(t testing.TB) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "claude")
	cmd := exec.Command("go", "build", "-o", binary, filepath.Join(root(), "internal", "claudeprovider", "testdata", "fakeclaude"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake claude: %v\n%s", err, out)
	}
	// Do not write a caller's FAKE_CLAUDE_ARGS or use its version override.
	warm := exec.CommandContext(t.Context(), binary, "--version")
	warm.Env = []string{}
	if out, err := warm.CombinedOutput(); err != nil {
		t.Fatalf("warm fake claude: %v\n%s", err, out)
	}
	return binary
}

// Fixture returns the absolute path of a recorded fixture, e.g. "plain.jsonl".
func Fixture(name string) string {
	return filepath.Join(root(), "internal", "claudeprovider", "testdata", Version, name)
}

// Command runs the fake replaying fixture.
func Command(binary, fixture string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "FAKE_CLAUDE_FIXTURE="+fixture)
	return cmd
}
