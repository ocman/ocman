// Package claudetest builds the fake claude executable that replays the
// spike's stream-json fixtures. Tests use it instead of the real CLI.
package claudetest

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
)

// Version is the CLI version the fixtures were recorded with.
const Version = "2.1.220"

func root() string {
	_, source, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "..")
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

// Binary returns a caller-owned fake as "claude" in the test's temp dir. The executable is built and warmed once per test binary (macOS
// first-exec latency must not consume the production version-probe or
// readiness deadline) and hard-linked into the caller's temp dir.
// Packages using Binary must call Run from TestMain to remove the shared build.
func Binary(t testing.TB) string {
	t.Helper()
	return InstallBinary(t, filepath.Join(t.TempDir(), "claude"))
}

// InstallBinary places the fake CLI at binary and returns it. The path may be
// on another filesystem than the shared build (a RAM-backed TMPDIR beside a
// disk /tmp), where a hard link or rename would fail.
func InstallBinary(t testing.TB, binary string) string {
	t.Helper()
	fakeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeErr = err
			return
		}
		fakeShared = filepath.Join(dir, "claude")
		cmd := exec.Command("go", "build", "-o", fakeShared, filepath.Join(root(), "internal", "platforms", "claude", "provider", "testdata", "fakeclaude"))
		cmd.Env = buildEnv
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("build fake claude: %w\n%s", err, out)
			return
		}
		// Do not write a caller's FAKE_CLAUDE_ARGS or use its version override.
		warm := exec.Command(fakeShared, "--version")
		warm.Env = []string{}
		if out, err := warm.CombinedOutput(); err != nil {
			fakeErr = fmt.Errorf("warm fake claude: %w\n%s", err, out)
		}
	})
	if fakeErr != nil {
		t.Fatal(fakeErr)
	}
	if err := os.Link(fakeShared, binary); err != nil { // cross-device: copy
		data, rerr := os.ReadFile(fakeShared)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if werr := os.WriteFile(binary, data, 0o700); werr != nil {
			t.Fatal(werr)
		}
	}
	return binary
}

// Run executes the suite and removes its shared build, even if tests or the
// build failed. Caller-owned links remain valid after removing the shared link.
func Run(m *testing.M) int {
	code := m.Run()
	if fakeShared != "" {
		if err := os.RemoveAll(filepath.Dir(fakeShared)); err != nil {
			fmt.Fprintln(os.Stderr, "remove fake claude build:", err)
			return 1
		}
	}
	return code
}

var (
	fakeOnce   sync.Once
	fakeShared string
	fakeErr    error
	// buildEnv is the real environment, captured before any test's
	// t.Setenv("HOME", …) can send go build to a cold, HOME-based cache.
	buildEnv = os.Environ()
)

// Fixture returns the absolute path of a recorded fixture, e.g. "plain.jsonl".
func Fixture(name string) string {
	return filepath.Join(root(), "internal", "platforms", "claude", "provider", "testdata", Version, name)
}

// Command runs the fake replaying fixture.
func Command(binary, fixture string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	cmd.Env = append(os.Environ(), "FAKE_CLAUDE_FIXTURE="+fixture)
	return cmd
}
