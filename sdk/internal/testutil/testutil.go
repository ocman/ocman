// Package testutil holds test helpers for the SDK module. It mirrors the
// executable helpers in ocman's internal/testutil, which the SDK module cannot
// import.
package testutil

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

const warmupEnv = "OCMAN_TEST_SCRIPT_WARMUP"

// WriteScript writes an executable shell script and runs it once, as a
// no-op, before returning.
//
// macOS assesses every newly written executable on its first exec, which
// can take seconds. A fake binary called under a short timeout would
// otherwise fail on that assessment, not on the behaviour under test.
// script must start with a "#!" line.
func WriteScript(t *testing.T, path, script string) {
	t.Helper()
	shebang, body, ok := strings.Cut(script, "\n")
	if !ok || !strings.HasPrefix(shebang, "#!") {
		t.Fatalf("WriteScript: %s has no #! line", path)
	}
	guarded := shebang + "\n[ -n \"$" + warmupEnv + "\" ] && exit 0\n" + body
	if err := os.WriteFile(path, []byte(guarded), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	cmd := exec.Command(path)
	cmd.Env = append(os.Environ(), warmupEnv+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("warm up %s: %v: %s", path, err, out)
	}
}

// WarmExecutable runs a freshly built binary once and ignores how it exits,
// so the first-exec assessment WriteScript describes happens before a test
// starts timing it.
func WarmExecutable(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(path)
	cmd.Stdin = nil
	_ = cmd.Run()
}
