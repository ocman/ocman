package term

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/tmux"

	"github.com/NoUseFreak/ocman/internal/testutil"
)

// Use short socket paths on macOS and never touch a developer's tmux servers.
func isolateTmux(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not available")
	}
	dir := testutil.ShortTempDir(t, "ocman-term-") // short: unix socket path limit
	t.Setenv("TMUX_TMPDIR", dir)
	t.Setenv("TMUX", "")
	t.Setenv("TERM", "xterm-256color")
	// Terminal fixtures must not run a developer's interactive shell startup.
	t.Setenv("SHELL", "/bin/sh")
	script := fmt.Sprintf("#!/bin/sh\nexec %q -f /dev/null \"$@\"\n", bin)
	testutil.WriteScript(t, filepath.Join(dir, "tmux"), script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		for _, socket := range []string{"default", "ocman-term"} {
			_ = exec.Command(bin, "-L", socket, "kill-server").Run()
		}
	})
	return bin
}

func TestShellExitClosesViewer(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	win, err := CreateWindow(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	other, err := CreateWindow(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	conn := newFakeTermConn()
	t.Cleanup(func() { _ = conn.Close() })
	done := make(chan error, 1)
	go func() { done <- AttachLocalPTY(t.Context(), hostsvc.TermAttachRequest{Dir: dir, Window: win}, conn) }()
	deadline := time.After(3 * time.Second)
	for len(conn.output()) == 0 {
		select {
		case <-deadline:
			t.Fatal("viewer did not attach")
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	conn.frames <- hostsvc.TermFrame{Data: []byte("exit\r")}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("exit left viewer attached to another window")
	}
	wins, err := Windows(t.Context(), dir)
	if err != nil || len(wins) != 1 || wins[0].Name != other {
		t.Fatalf("windows after exit = %v, %v; want only %s", wins, err, other)
	}
	assertNoViewers(t)
}

func assertNoViewers(t *testing.T) {
	t.Helper()
	out, err := tmux.Output(t.Context(), "-L", socketName, "list-sessions", "-F", "#{session_name}")
	if err != nil || strings.TrimSpace(string(out)) != SessionName {
		t.Fatalf("sessions = %q, %v; want only %s", out, err, SessionName)
	}
}

func TestViewerCreationFailureCleansUp(t *testing.T) {
	isolateTmux(t)
	if _, err := CreateWindow(t.Context(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := createViewer(t.Context(), SessionName+":missing-window"); err == nil {
		t.Fatal("expected missing window error")
	}
	assertNoViewers(t)
}

func TestViewerDetachKeepsShell(t *testing.T) {
	isolateTmux(t)
	dir := t.TempDir()
	win, err := CreateWindow(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		conn := newFakeTermConn()
		t.Cleanup(func() { _ = conn.Close() })
		done := make(chan error, 1)
		go func() { done <- AttachLocalPTY(t.Context(), hostsvc.TermAttachRequest{Dir: dir, Window: win}, conn) }()
		deadline := time.After(3 * time.Second)
		for len(conn.output()) == 0 {
			select {
			case <-deadline:
				t.Fatal("viewer did not attach")
			default:
				time.Sleep(10 * time.Millisecond)
			}
		}
		_ = conn.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("viewer did not detach")
		}
		wins, err := Windows(t.Context(), dir)
		if err != nil || len(wins) != 1 || wins[0].Name != win {
			t.Fatalf("windows = %v, %v", wins, err)
		}
		assertNoViewers(t)
	}
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
