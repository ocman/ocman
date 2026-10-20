package app

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms/claudecode"
)

// TestHookCommandExitsZeroWhenOcmanIsDown runs `ocman hook` as a process
// against a listener that never answers, with stdin left open.
func TestHookCommandExitsZeroWhenOcmanIsDown(t *testing.T) {
	t.Setenv("OCMAN_MANAGED", "1")
	if addr := os.Getenv("OCMAN_TEST_HOOK_ADDR"); addr != "" {
		os.Args = []string{"ocman", "hook", "--addr", addr, "Stop"}
		Run("dev", skillCatalog)
		return
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	stdin, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	defer stdin.Close()
	if _, err := w.WriteString(`{"session_id":"s1"}`); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestHookCommandExitsZeroWhenOcmanIsDown$")
	cmd.Env = append(os.Environ(), "OCMAN_TEST_HOOK_ADDR="+ln.Addr().String())
	cmd.Stdin = stdin
	start := time.Now()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("hook exited with %v\n%s", err, out)
	}
	if d := time.Since(start); d > 3*time.Second { // ~1 s hook plus test-binary startup
		t.Fatalf("hook took %v", d)
	}
}

func TestRunHookCommandArgs(t *testing.T) {
	t.Setenv("OCMAN_MANAGED", "1")
	if claudecode.RunHookCommand([]string{"serve"}, strings.NewReader("")) || claudecode.RunHookCommand(nil, nil) {
		t.Fatal("non-hook args handled")
	}
	// Bad usage still succeeds: a hook must never fail Claude.
	if !claudecode.RunHookCommand([]string{"hook", "--bogus"}, strings.NewReader("")) || !claudecode.RunHookCommand([]string{"hook"}, strings.NewReader("")) {
		t.Fatal("hook usage not handled")
	}
}

// TestClaudeHookPlugin installs the hook plugin through Settings' API with
// the fake CLI, then checks that terminal hooks do not change status.
func TestClaudeHookPlugin(t *testing.T) {
	t.Setenv("OCMAN_MANAGED", "")
	if runOcmanChild() {
		return
	}
	dir := t.TempDir()
	config := filepath.Join(dir, ".claude")
	copyTree(t, filepath.Join("..", "claudeprovider", "testdata", "projects"), filepath.Join(config, "projects"))
	settings := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	claudetest.InstallBinary(t, filepath.Join(bin, "claude"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mcp := ln.Addr().String()
	_ = ln.Close()
	base, out, stop := startClaudeOcman(t, "TestClaudeHookPlugin", dir, "OCMAN_TEST_MCP_ADDR="+mcp, "CLAUDE_CONFIG_DIR="+config)
	defer stop()

	identity := waitSessionIdentity(t, base, "claudecode", "22222222-2222-4222-8222-222222222222")
	status := func() db.SessionStatus {
		for _, s := range listSessions(base) {
			if s.ID == identity.ID {
				return s.Status
			}
		}
		return ""
	}
	waitStatus := func(want db.SessionStatus) {
		t.Helper()
		for deadline := time.Now().Add(30 * time.Second); status() != want; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("status = %q, want %q\n%s", status(), want, out.String())
			}
		}
	}
	// An unfinished transcript nothing observes is not running, however fresh (AD-10).
	waitStatus(db.StatusInterrupted)

	var plugin map[string]bool
	resp, err := http.Post(base+"/api/hook-plugin/install", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.NewDecoder(resp.Body).Decode(&plugin)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !plugin["available"] || !plugin["installed"] {
		t.Fatalf("install: %d %v\n%s", resp.StatusCode, plugin, out.String())
	}
	hooks, err := os.ReadFile(filepath.Join(dir, ".local", "share", "ocman", "claude-marketplace", "ocman", "hooks", "hooks.json"))
	// History hooks record terminal sessions in the isolated test journal.
	if err != nil || !bytes.Contains(hooks, []byte("hook --addr "+mcp+" --journal '")) || !bytes.Contains(hooks, []byte("/ocman/journal/claudecode' Stop 2")) {
		t.Fatalf("hooks.json = %s, %v", hooks, err)
	}

	hook := func(event string) {
		if !claudecode.RunHookCommand([]string{"hook", "--addr", mcp, event}, strings.NewReader(`{"session_id":"22222222-2222-4222-8222-222222222222"}`)) {
			t.Fatal("hook not handled")
		}
	}
	hook("Stop")
	hook("PreToolUse")
	waitStatus(db.StatusInterrupted)

	resp, err = http.Post(base+"/api/hook-plugin/uninstall", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if data, _ := os.ReadFile(settings); resp.StatusCode != http.StatusOK || !bytes.Equal(bytes.Join(bytes.Fields(data), nil), []byte(`{"theme":"dark"}`)) {
		t.Fatalf("uninstall: %d, settings %s", resp.StatusCode, data)
	}
}
