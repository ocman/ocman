package claudeprovider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestExternalOwnerWarning(t *testing.T) {
	l := &live{opts: Options{StatePath: filepath.Join(t.TempDir(), "owned.json"), ConfigDir: t.TempDir()}}
	event := providerprotocol.HookEvent{Event: "SessionStart", SessionID: "external", PID: os.Getpid()}
	l.observeExternal(event)
	if len(l.externalWarnings("external")) != 1 || len(l.externalWarnings("other")) != 0 {
		t.Fatal("warning must be scoped to the observed session")
	}
	// Both the browsing and project supervisors see the same observation.
	project := &live{opts: l.opts}
	if len(project.externalWarnings("external")) != 1 {
		t.Fatal("project supervisor lost the warning")
	}
	project.opts.ConfigDir = t.TempDir()
	if len(project.externalWarnings("external")) != 0 {
		t.Fatal("warning leaked across Claude configurations")
	}
	// Stop ends a turn, not the external process.
	event.Event = "Stop"
	l.observeExternal(event)
	if len(l.externalWarnings("external")) != 1 {
		t.Fatal("idle external instance lost its warning")
	}
	event.Event = "SessionEnd"
	l.observeExternal(event)
	if len(l.externalWarnings("external")) != 0 {
		t.Fatal("warning remained after external session exited")
	}
}

func TestExternalOwnerCrashAndFallback(t *testing.T) {
	l := &live{opts: Options{StatePath: filepath.Join(t.TempDir(), "owned.json")}}
	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	l.observeExternal(providerprotocol.HookEvent{Event: "SessionStart", SessionID: "crash", PID: child.Process.Pid})
	if len(l.externalWarnings("crash")) != 1 {
		t.Fatal("live child not detected")
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	if len(l.externalWarnings("crash")) != 0 {
		t.Fatal("warning remained after process died without SessionEnd")
	}
	if !(externalOwner{At: now()}).alive() || (externalOwner{At: time.Now().Add(-time.Hour).UnixMilli()}).alive() {
		t.Fatal("PID-less observations must expire")
	}
	l.observeExternal(providerprotocol.HookEvent{Event: "PreToolUse", SessionID: "old-hook"})
	if len(l.externalWarnings("old-hook")) != 1 {
		t.Fatal("older hooks must still give a best-effort warning")
	}
}

func TestExternalOwnerIgnoresManagedAndBadObservations(t *testing.T) {
	l := &live{opts: Options{StatePath: filepath.Join(t.TempDir(), "owned.json")}}
	for _, event := range []providerprotocol.HookEvent{
		{Event: "SessionStart", SessionID: "managed", PID: os.Getpid(), Managed: true},
		{Event: "SessionStart", SessionID: "invalid", PID: -1},
	} {
		l.observeExternal(event)
		if len(l.externalWarnings(event.SessionID)) != 0 {
			t.Fatal("non-external observation produced warning")
		}
	}
	dir := l.externalOwnerDir("malformed")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bad.json", ".owner-incomplete"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if len(l.externalWarnings("malformed")) != 0 {
		t.Fatal("malformed observation produced warning")
	}
	// I/O failures are never continuation gates.
	l.opts.StatePath = filepath.Join(t.TempDir(), "file", "owned.json")
	if err := os.WriteFile(filepath.Dir(l.opts.StatePath), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	l.observeExternal(providerprotocol.HookEvent{Event: "SessionStart", SessionID: "unwritable"})
	if len(l.externalWarnings("unwritable")) != 0 {
		t.Fatal("failed observation produced warning")
	}
	l.opts.StatePath = ""
	l.observeExternal(providerprotocol.HookEvent{Event: "SessionStart", SessionID: "disabled"})
	if len(l.externalWarnings("disabled")) != 0 {
		t.Fatal("warning storage must be disabled without a state path")
	}
}

func TestFindClaudePID(t *testing.T) {
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		lookup func(context.Context, int) (int, string, error)
		want   int
	}{
		{"shell-parent", t.Context(), func(_ context.Context, pid int) (int, string, error) {
			if pid == 100 {
				return 90, "/bin/sh", nil
			}
			return 1, "/usr/bin/claude", nil
		}, 90},
		{"versioned", t.Context(), func(context.Context, int) (int, string, error) { return 1, "claude-2.1.220", nil }, 100},
		{"native-install", t.Context(), func(context.Context, int) (int, string, error) {
			return 1, "/home/u/.local/share/claude/versions/2.1.220", nil
		}, 100},
		{"unavailable", t.Context(), func(context.Context, int) (int, string, error) { return 0, "", errors.New("gone") }, 0},
		{"loop", t.Context(), func(_ context.Context, pid int) (int, string, error) { return pid, "sh", nil }, 0},
		{"bounded", t.Context(), func(_ context.Context, pid int) (int, string, error) { return pid - 1, "sh", nil }, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := findClaudePID(tc.ctx, 100, tc.lookup); got != tc.want {
				t.Fatalf("PID = %d, want %d", got, tc.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got := hookClaudePID(ctx); got != 0 {
		t.Fatalf("cancelled lookup = %d", got)
	}
}
