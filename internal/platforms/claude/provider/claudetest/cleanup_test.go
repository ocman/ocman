package claudetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProcessCleanup(t *testing.T) {
	if dir := os.Getenv("CLAUDETEST_CLEANUP_CHILD"); dir != "" {
		defer func() {
			if err := os.WriteFile(filepath.Join(dir, "shared"), []byte(filepath.Dir(fakeShared)), 0o600); err != nil {
				t.Error(err)
			}
		}()
		mode := os.Getenv("CLAUDETEST_CLEANUP_MODE")
		if mode == "build failure" {
			buildEnv = append(buildEnv, "GOROOT="+filepath.Join(dir, "missing"))
		}
		binary := Binary(t)
		if err := os.Link(binary, filepath.Join(dir, "caller")); err != nil {
			t.Fatal(err)
		}
		if mode == "test failure" {
			t.Fatal("intentional test failure")
		}
		return
	}
	for _, mode := range []string{"success", "test failure", "build failure"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			cmd := exec.Command(os.Args[0], "-test.run=^TestProcessCleanup$")
			cmd.Env = append(os.Environ(), "CLAUDETEST_CLEANUP_CHILD="+dir, "CLAUDETEST_CLEANUP_MODE="+mode)
			out, err := cmd.CombinedOutput()
			if (err == nil) != (mode == "success") {
				t.Fatalf("child exit: %v\n%s", err, out)
			}
			shared, err := os.ReadFile(filepath.Join(dir, "shared"))
			if err != nil {
				t.Fatal(err)
			}
			// Clean up the leaked directory too when testing the unfixed code.
			t.Cleanup(func() { _ = os.RemoveAll(string(shared)) })
			if _, err := os.Stat(string(shared)); !os.IsNotExist(err) {
				t.Errorf("shared build directory remains after child exit: %v", err)
			}
			if mode != "build failure" {
				out, err := exec.Command(filepath.Join(dir, "caller"), "--version").CombinedOutput()
				if err != nil || strings.TrimSpace(string(out)) != Version+" (Claude Code)" {
					t.Fatalf("caller link stopped working: %q, %v", out, err)
				}
			}
		})
	}
}
