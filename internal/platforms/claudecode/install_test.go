package claudecode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallHooks_FreshFile writes the hooks block from scratch when
// the user has no existing settings.json.
func TestInstallHooks_FreshFile(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")

	if err := InstallHooks(settings, "http://127.0.0.1:9999/api/hooks/claude"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	got := readJSON(t, settings)
	hooks, ok := got["hooks"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing 'hooks' in %+v", got)
	}

	// Every event we manage must have exactly one entry, tagged
	// as ours.
	for _, event := range managedHookEvents {
		entries, ok := hooks[event].([]interface{})
		if !ok || len(entries) == 0 {
			t.Errorf("event %s: entries missing or wrong type: %v", event, hooks[event])
			continue
		}
		if len(entries) != 1 {
			t.Errorf("event %s: expected exactly 1 entry, got %d", event, len(entries))
		}
		entry, ok := entries[0].(map[string]interface{})
		if !ok {
			t.Errorf("event %s: entry is not an object: %T", event, entries[0])
			continue
		}
		if entry["_owner"] != ocmanHookOwner {
			t.Errorf("event %s: _owner = %v, want %q", event, entry["_owner"], ocmanHookOwner)
		}
		// Matcher is always present for shape consistency with
		// Claude Code's examples.
		if entry["matcher"] == nil {
			t.Errorf("event %s: matcher missing", event)
		}
		inner, ok := entry["hooks"].([]interface{})
		if !ok || len(inner) != 1 {
			t.Errorf("event %s: inner hooks array wrong shape: %v", event, entry["hooks"])
			continue
		}
		cmd, _ := inner[0].(map[string]interface{})
		if cmd["type"] != "command" {
			t.Errorf("event %s: type = %v, want command", event, cmd["type"])
		}
		cmdStr, _ := cmd["command"].(string)
		if !strings.Contains(cmdStr, "http://127.0.0.1:9999/api/hooks/claude") {
			t.Errorf("event %s: command missing url: %q", event, cmdStr)
		}
		if !strings.Contains(cmdStr, "--data-binary @-") {
			t.Errorf("event %s: command missing stdin-piping flag: %q", event, cmdStr)
		}
	}
}

// TestInstallHooks_PreservesUnrelatedKeys must not stomp on other
// top-level settings fields the user has configured.
func TestInstallHooks_PreservesUnrelatedKeys(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	writeJSON(t, settings, map[string]interface{}{
		"effortLevel": "high",
		"env":         map[string]string{"FOO": "bar"},
	})

	if err := InstallHooks(settings, "http://localhost:1/api/hooks/claude"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	got := readJSON(t, settings)
	if got["effortLevel"] != "high" {
		t.Errorf("effortLevel clobbered: %v", got["effortLevel"])
	}
	env, ok := got["env"].(map[string]interface{})
	if !ok || env["FOO"] != "bar" {
		t.Errorf("env clobbered: %v", got["env"])
	}
	if _, ok := got["hooks"]; !ok {
		t.Error("hooks block should have been added")
	}
}

// TestInstallHooks_PreservesUserHooks merges alongside user-authored
// entries: our entry sits next to theirs, not in place of it.
func TestInstallHooks_PreservesUserHooks(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	userHook := map[string]interface{}{
		"matcher": "Bash",
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": "echo 'user-managed'",
			},
		},
	}
	writeJSON(t, settings, map[string]interface{}{
		"hooks": map[string]interface{}{
			"PreToolUse": []interface{}{userHook},
		},
	})

	if err := InstallHooks(settings, "http://localhost:1/api/hooks/claude"); err != nil {
		t.Fatalf("InstallHooks: %v", err)
	}

	got := readJSON(t, settings)
	hooks := got["hooks"].(map[string]interface{})
	pre := hooks["PreToolUse"].([]interface{})

	// Exactly two entries: the user's + ours.
	if len(pre) != 2 {
		t.Fatalf("expected 2 PreToolUse entries (user + ocman), got %d", len(pre))
	}
	ownedCount := 0
	userCount := 0
	for _, e := range pre {
		m := e.(map[string]interface{})
		if m["_owner"] == ocmanHookOwner {
			ownedCount++
		} else {
			userCount++
		}
	}
	if ownedCount != 1 || userCount != 1 {
		t.Errorf("expected 1 ocman + 1 user entry, got %d + %d", ownedCount, userCount)
	}
}

// TestInstallHooks_ReplacesStaleOcmanEntry re-running the installer
// with a different URL must replace our previous entry, not
// accumulate duplicates.
func TestInstallHooks_ReplacesStaleOcmanEntry(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")

	if err := InstallHooks(settings, "http://localhost:1/api/hooks/claude"); err != nil {
		t.Fatalf("first install: %v", err)
	}
	if err := InstallHooks(settings, "http://localhost:2/api/hooks/claude"); err != nil {
		t.Fatalf("second install: %v", err)
	}

	got := readJSON(t, settings)
	hooks := got["hooks"].(map[string]interface{})
	for _, event := range managedHookEvents {
		entries := hooks[event].([]interface{})
		if len(entries) != 1 {
			t.Errorf("event %s: expected 1 entry after reinstall, got %d", event, len(entries))
			continue
		}
		inner := entries[0].(map[string]interface{})["hooks"].([]interface{})
		cmd := inner[0].(map[string]interface{})["command"].(string)
		if !strings.Contains(cmd, "http://localhost:2/api/hooks/claude") {
			t.Errorf("event %s: command not updated: %q", event, cmd)
		}
	}
}

// TestInstallHooks_IdempotentOnIdenticalInput a no-op second install
// (same URL) must not rewrite the file — stable mtime helps editors
// and VCS watches not flap.
func TestInstallHooks_IdempotentOnIdenticalInput(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	url := "http://localhost:1/api/hooks/claude"

	if err := InstallHooks(settings, url); err != nil {
		t.Fatalf("first install: %v", err)
	}
	info1, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}

	// Briefly sleep so that if the installer does rewrite, the
	// mtime would measurably change on most filesystems.
	time0 := info1.ModTime()

	if err := InstallHooks(settings, url); err != nil {
		t.Fatalf("second install: %v", err)
	}
	info2, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(time0) {
		t.Errorf("file rewritten on idempotent re-install (mtime %v -> %v)", time0, info2.ModTime())
	}
}

// TestInstallHooks_RejectsBadJSONInFile returns an error rather than
// overwriting a user's hand-edited settings that happens to be broken.
// Clobbering broken config would destroy hours of user tweaks; safer
// to fail and log.
func TestInstallHooks_RejectsBadJSONInFile(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := InstallHooks(settings, "http://localhost:1/api/hooks/claude")
	if err == nil {
		t.Error("expected error on malformed existing settings.json")
	}
}

func readJSON(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal %s: %v\n%s", path, err, b)
	}
	return m
}

func writeJSON(t *testing.T, path string, v interface{}) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
