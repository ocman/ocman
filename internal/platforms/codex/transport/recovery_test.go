package codextransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func TestRestartChangesCodexHome(t *testing.T) {
	r := newRig(t)
	ctx := t.Context()
	first := r.platform(ctx)
	res, err := first.EnsureRuntime(ctx, "/repo")
	if err != nil {
		t.Fatal(err)
	}
	identity := first.mgr.Runtime.Identity()
	if _, err := first.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	first.Close()
	// Model a tmux server retaining the previous dashboard's environment.
	env := filepath.Join(r.data, "tmux-env")
	if err := os.WriteFile(env, []byte("FAKE_CODEX=1\nCODEX_HOME="+os.Getenv("CODEX_HOME")+"\nFAKE_CODEX_LAUNCHES="+os.Getenv("FAKE_CODEX_LAUNCHES")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCMAN_FAKE_TMUX_ENV", env)
	home := t.TempDir()
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", link)
	second := r.platform(ctx)
	if second.mgr.Runtime.Identity() == identity {
		t.Fatal("changed Codex home reused the launch identity")
	}
	next, err := second.EnsureRuntime(ctx, "/repo")
	if err != nil || next.Endpoint == res.Endpoint {
		t.Fatalf("home change did not replace supervisor: %+v %v", next, err)
	}
	if _, err := second.HistoryInventory(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := second.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(home)
	lines, _ := os.ReadFile(filepath.Join(r.data, "launches"))
	if strings.Count(string(lines), canonical+" ") != 2 {
		t.Fatalf("supervisor and history must explicitly use canonical home %q: %s", canonical, lines)
	}
}

func TestMCPListenerRebind(t *testing.T) {
	r := newRig(t)
	p := r.platform(t.Context())
	p.SetMCPAddr("127.0.0.1:9001")
	first, err := p.EnsureRuntime(t.Context(), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	p.Close()
	p = r.platform(t.Context())
	p.SetMCPAddr("127.0.0.1:9002")
	next, err := p.EnsureRuntime(t.Context(), "/repo")
	if err != nil || next.Endpoint == first.Endpoint {
		t.Fatalf("listener rebind reused stale supervisor: %+v %v", next, err)
	}
	if _, err := p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	lines, _ := os.ReadFile(filepath.Join(r.data, "launches"))
	if !strings.Contains(string(lines), "127.0.0.1:9002/mcp") {
		t.Fatalf("replacement did not receive listener: %s", lines)
	}
	// Rebinding a running dashboard must also replace its attached adapter.
	p.SetMCPAddr("127.0.0.1:9003")
	p.SetMCPAddr("127.0.0.1:9003") // repeated notification is harmless
	_, err = p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	if errors.Is(err, platforms.ErrBusy) {
		// Recovery may already have closed admission while draining.
		if _, err := p.EnsureRuntime(t.Context(), "/repo"); err != nil {
			t.Fatal(err)
		}
		_, err = p.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo"})
	}
	if err != nil {
		t.Fatal(err)
	}
	lines, _ = os.ReadFile(filepath.Join(r.data, "launches"))
	if !strings.Contains(string(lines), "127.0.0.1:9003/mcp") {
		t.Fatalf("attached supervisor kept the old listener: %s", lines)
	}
}

func TestReplacementKeepsPendingApprovalReachable(t *testing.T) {
	r := newRig(t)
	t.Setenv("FAKE_CODEX_APPROVAL", "1")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	first := r.platform(ctx)
	if _, err := first.CreateSession(ctx, platforms.CreateSessionRequest{Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	if err := first.SendMessage(ctx, platforms.SendMessageRequest{SessionID: "thr", Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	// Reconcile until Codex has asked, then restart without answering it.
	for {
		prompts, err := first.RefreshPermissions(ctx, "thr")
		if err == nil && len(prompts) > 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("fake Codex did not ask for approval")
		}
		time.Sleep(10 * time.Millisecond)
	}
	first.Close()
	t.Setenv("CODEX_HOME", t.TempDir())
	second := r.platform(ctx)
	go func() { _ = second.WatchEvents(ctx) }()
	if second.mgr.OnDrain == nil {
		t.Fatal("replacement recovery has no draining prompt route")
	}
	for {
		prompts, err := second.ListPermissions(ctx, "thr")
		if err == nil && len(prompts) > 0 {
			id, _ := prompts[0]["id"].(string)
			if err := second.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: "thr", PermissionID: id, Reply: "once"}); err != nil {
				t.Fatal(err)
			}
			break
		}
		if ctx.Err() != nil {
			t.Fatal("pending approval unreachable during replacement")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := second.EnsureRuntime(ctx, "/repo"); err != nil {
		t.Fatalf("answered approval did not let replacement settle: %v", err)
	}
}
