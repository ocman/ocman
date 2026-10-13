package gitexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestCommandConfigDoesNotOverwriteInheritedConfigFile(t *testing.T) {
	caller, fixture := t.TempDir(), t.TempDir()
	for _, repo := range []string{caller, fixture} {
		isolatedGit(t, "init", repo)
	}
	config := filepath.Join(caller, ".git", "config")
	before, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG", config)
	if out, err := Command(t.Context(), "-C", fixture, "config", "user.email", "test@example.com").CombinedOutput(); err != nil {
		t.Fatalf("config: %v %s", err, out)
	}
	after, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("fixture identity write modified caller config")
	}
	if got := isolatedGit(t, "-C", fixture, "config", "--local", "user.email"); got != "test@example.com\n" {
		t.Fatalf("fixture identity=%q", got)
	}
}

// Fixture setup is independent of the environment cleaner under test, including
// any Git hook context inherited by the test process itself.
func isolatedGit(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0"}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}
