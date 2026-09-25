package term

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Use short socket paths on macOS and never touch a developer's tmux servers.
func isolateTmux(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir, err := os.MkdirTemp("/tmp", "ocman-term-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -f /dev/null \"$@\"\n", bin)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		for _, socket := range []string{"default", "ocman-term"} {
			_ = exec.Command(bin, "-L", socket, "kill-server").Run()
		}
	})
	return bin
}

func TestTerminalServerIsolation(t *testing.T) {
	bin := isolateTmux(t)
	if out, err := exec.Command(bin, "-L", "default", "-f", "/dev/null", "new-session", "-d", "-s", "user-session").CombinedOutput(); err != nil {
		t.Fatalf("start default server: %v: %s", err, out)
	}
	dir := t.TempDir()
	win, err := CreateWindow(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "-L", "default", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil || strings.TrimSpace(string(out)) != "user-session" {
		t.Fatalf("default server sessions = %q, %v; want only user-session", out, err)
	}
	if out, err := exec.Command(bin, "-L", "ocman-term", "has-session", "-t", SessionName).CombinedOutput(); err != nil {
		t.Fatalf("dedicated server missing terminal session: %v: %s", err, out)
	}
	wins, err := Windows(t.Context(), dir)
	if err != nil || len(wins) != 1 || wins[0].Name != win {
		t.Fatalf("Windows = %v, %v; want %s", wins, err, win)
	}
	if err := KillWindow(t.Context(), dir, win); err != nil {
		t.Fatal(err)
	}
	wins, err = Windows(t.Context(), dir)
	if err != nil || len(wins) != 0 {
		t.Fatalf("Windows after kill = %v, %v; want empty", wins, err)
	}
}
