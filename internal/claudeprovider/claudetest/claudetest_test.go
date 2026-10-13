package claudetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryWarmupDoesNotUseCallerEnvironment(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "args")
	t.Setenv("FAKE_CLAUDE_ARGS", marker)
	t.Setenv("FAKE_CLAUDE_VERSION", "unsupported")
	binary := Binary(t)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("warm-up wrote caller's args: %v", err)
	}
	out, err := exec.Command(binary, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "unsupported (Claude Code)" {
		t.Fatalf("caller override: %q, %v", out, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("real call did not write args: %v", err)
	}
}
