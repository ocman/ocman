package server

import (
	"os"
	"path/filepath"
	"testing"

	claudecodeplatform "github.com/NoUseFreak/ocman/internal/platforms/claudecode"
)

// TestHookURLFromAddr checks the listen-address -> hook-URL rewrite.
// Covers the realistic forms of -addr: "host:port", ":port" (any-iface),
// "0.0.0.0:port", and "[::]:port". All must collapse to a localhost
// callback URL — Claude Code fires hooks from the user's own shell,
// so the loopback interface is always reachable regardless of how
// ocman was bound.
func TestHookURLFromAddr(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"127.0.0.1:8229", "http://127.0.0.1:8229/api/hooks/claude"},
		{"localhost:8229", "http://127.0.0.1:8229/api/hooks/claude"},
		{":8229", "http://127.0.0.1:8229/api/hooks/claude"},
		{"0.0.0.0:8229", "http://127.0.0.1:8229/api/hooks/claude"},
		{"[::]:8229", "http://127.0.0.1:8229/api/hooks/claude"},
		{"[::1]:8229", "http://127.0.0.1:8229/api/hooks/claude"},
	}
	for _, c := range cases {
		got := hookURLFromAddr(c.in)
		if got != c.want {
			t.Errorf("hookURLFromAddr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHookURLFromAddr_Invalid returns empty string when the addr
// isn't parseable — caller then skips the install silently.
func TestHookURLFromAddr_Invalid(t *testing.T) {
	if got := hookURLFromAddr("not a real addr"); got != "" {
		t.Errorf("expected empty for bogus addr, got %q", got)
	}
}

// TestMaybeInstallClaudeHooks_WritesSettingsWhenAdapterAvailable is
// the integration path: with a claude-code adapter pointed at a temp
// directory, a boot-time refresh should produce settings.json with
// our hook entries.
func TestMaybeInstallClaudeHooks_WritesSettingsWhenAdapterAvailable(t *testing.T) {
	// The boot path bails out when `claude` isn't on PATH (see
	// claude_boot.go), because installing hooks for a missing CLI
	// would produce dead config. CI runners don't have the Claude
	// CLI installed, so inject a dummy executable for this test.
	withFakeClaudeOnPath(t)

	// Lay out a fake ~/.claude/projects so the adapter reports
	// Available() and RefreshHooks has a parent directory to write
	// settings.json into.
	home := t.TempDir()
	projectsDir := filepath.Join(home, ".claude", "projects")
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	srv := testServer(t)
	srv.registry.Register(claudecodeplatform.NewFromDir(projectsDir))
	srv.addr = "127.0.0.1:19999"

	srv.maybeInstallClaudeHooks()

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("expected settings.json to exist at %s: %v", settingsPath, err)
	}
}

// withFakeClaudeOnPath drops a no-op `claude` executable into a temp
// directory and prepends it to PATH for the duration of t. Required
// because maybeInstallClaudeHooks refuses to write settings.json when
// the Claude CLI is missing from PATH.
func withFakeClaudeOnPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestMaybeInstallClaudeHooks_NoopWithoutAdapter silently skips when
// no claude-code adapter is registered. Server boot must not fail.
func TestMaybeInstallClaudeHooks_NoopWithoutAdapter(t *testing.T) {
	srv := testServer(t)
	srv.addr = "127.0.0.1:19999"
	// Only opencode is registered (by testServer). Should not panic
	// and should not touch the filesystem.
	srv.maybeInstallClaudeHooks()
}
