package testutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsolateHostDropsAgentEnvironment(t *testing.T) {
	// Restore everything IsolateHost changes once the test ends.
	saved := os.Environ()
	t.Cleanup(func() {
		os.Clearenv()
		for _, kv := range saved {
			key, value, _ := strings.Cut(kv, "=")
			_ = os.Setenv(key, value)
		}
	})
	home, _ := os.UserHomeDir()
	goCache := realGoEnv()["GOCACHE"]
	_ = os.Setenv("TMUX", "/tmp/tmux-501/ocman-agents,1,0")
	_ = os.Setenv("TMUX_PANE", "%1")
	_ = os.Setenv("OPENCODE_DB", filepath.Join(home, ".local", "share", "opencode", "opencode.db"))
	_ = os.Setenv("OPENCODE_SERVER_PASSWORD", "secret")
	_ = os.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	_ = os.Unsetenv(isolatedHostEnv)
	cleanup := IsolateHost()
	root := os.Getenv("HOME")
	// A re-executed child keeps its parent's choices.
	_ = os.Setenv("HOME", "/child")
	IsolateHost()()
	if os.Getenv("HOME") != "/child" {
		t.Errorf("nested IsolateHost moved HOME to %q", os.Getenv("HOME"))
	}
	_ = os.Setenv("HOME", root)
	for _, key := range []string{"TMUX", "TMUX_PANE", "OPENCODE_DB", "OPENCODE_SERVER_PASSWORD", "XDG_DATA_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			t.Errorf("%s = %q, want unset", key, value)
		}
	}
	if root == home || len(root) > len("/private/tmp/h-0123456789") {
		t.Errorf("HOME = %q, want a short temporary directory", root)
	}
	if dir := os.Getenv("TMUX_TMPDIR"); !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Errorf("TMUX_TMPDIR = %q, want under %s", dir, root)
	} else if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("TMUX_TMPDIR directory: %v", err)
	}
	if goCache != "" && os.Getenv("GOCACHE") != goCache {
		t.Errorf("GOCACHE = %q, want the real cache %q", os.Getenv("GOCACHE"), goCache)
	}

	// A tmux server a test left behind is killed with the directory.
	bin, log := t.TempDir(), filepath.Join(t.TempDir(), "tmux.log")
	script := "#!/bin/sh\necho \"$*\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "tmux"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.Setenv("PATH", bin)
	socket := filepath.Join(os.Getenv("TMUX_TMPDIR"), "tmux-501", "ocman-agents")
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if data, err := os.ReadFile(log); err != nil || strings.TrimSpace(string(data)) != "-S "+socket+" kill-server" {
		t.Fatalf("tmux calls = %q, %v", data, err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("isolated root still exists: %v", err)
	}
}
