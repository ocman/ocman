package codextransport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestParseSupervisorArgs(t *testing.T) {
	a, err := parseSupervisorArgs([]string{"--socket", "/s", "--spool", "/sp", "--repo", "/r", "--codex-cli", "/bin/codex", "--codex-home", "/codex-home", "--mcp-addr", "127.0.0.1:9", "--ocman-version", "1.2"})
	if err != nil || a.CLI != "/bin/codex" || a.Version != "1.2" {
		t.Fatalf("args = %+v %v", a, err)
	}
	opts := a.options()
	if opts.Dir != "/r" || !slices.Contains(opts.Env, "CODEX_HOME=/codex-home") || !slices.Equal(opts.Args, []string{"-c", `mcp_servers.ocman.url="http://127.0.0.1:9/mcp"`}) {
		t.Fatalf("options = %+v", opts)
	}
	a.MCPAddr = ""
	if a.options().Args != nil {
		t.Fatal("MCP override without an address")
	}
	for _, args := range [][]string{
		{"--socket", "rel", "--spool", "/sp", "--repo", "/r"},
		{"--socket", "/s", "--spool", "/sp", "--repo", "/r", "extra"},
		{"--unknown"},
	} {
		if _, err := parseSupervisorArgs(args); err == nil {
			t.Errorf("parse(%q) succeeded", args)
		}
	}
	if err := runSupervisor(context.Background(), []string{"--socket", "rel"}); err == nil {
		t.Fatal("runSupervisor accepted bad args")
	}
	// A spool below a regular file cannot be created.
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSupervisor(context.Background(), []string{"--socket", "/s", "--spool", filepath.Join(file, "spool"), "--repo", "/r"}); err == nil {
		t.Fatal("runSupervisor started without a spool")
	}
	for _, args := range [][]string{nil, {"provider"}, {"provider", "claudecode"}, {"hook", "codex"}} {
		if RunProviderCommand(args) {
			t.Errorf("RunProviderCommand(%q) handled another command", args)
		}
	}
}

func TestNew(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	p := New(context.Background(), Config{DataDir: t.TempDir(), Version: "test"})
	if p == nil {
		t.Fatal("New failed")
	}
	p.(*Platform).Close()
	t.Setenv("CODEX_HOME", filepath.Join(t.TempDir(), "missing"))
	if p := New(context.Background(), Config{DataDir: t.TempDir()}); p != nil {
		t.Fatal("New succeeded with an unavailable Codex home")
	}
	t.Setenv("CODEX_HOME", t.TempDir())
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if p := New(context.Background(), Config{DataDir: file}); p != nil {
		t.Fatal("New succeeded below a file")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "codex-rules.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p := New(context.Background(), Config{DataDir: dir}); p != nil {
		t.Fatal("New succeeded with unreadable rules")
	}
}

func TestForwarding(t *testing.T) {
	r := newRig(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p := r.platform(ctx)
	p.SetMCPAddr("127.0.0.1:9")
	if args := p.mgr.Runtime.Args(); !slices.Contains(args, "--mcp-addr") || !slices.Contains(args, "127.0.0.1:9") {
		t.Fatalf("launch args = %q", args)
	}
	// Before a supervisor runs, nothing is pending and nothing launches.
	if !p.ReplyOutcomes(ctx, "thr") {
		t.Fatal("reply outcomes before launch")
	}
	if prompts, err := p.RefreshPermissions(ctx, "thr"); err != nil || prompts != nil {
		t.Fatalf("refresh before launch = %v %v", prompts, err)
	}
	if prompts, err := p.ListQuestions(ctx, "thr"); err != nil || prompts != nil {
		t.Fatalf("questions before launch = %v %v", prompts, err)
	}
	p.SetEventHandler(func(providerprotocol.Event) {})
	if p.current() != nil {
		t.Fatal("a read launched the supervisor")
	}

	if _, err := p.EnsureRuntime(ctx, "/repo"); err != nil {
		t.Fatal(err)
	}
	p.SetEventHandler(func(providerprotocol.Event) {}) // applies to the attached adapter too
	for name, err := range map[string]error{
		"permission": p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: "thr", PermissionID: "per_x", Reply: "once"}),
		"question":   p.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: "thr", RequestID: "que_x"}),
		"reject":     p.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: "thr", RequestID: "que_x"}),
	} {
		if err == nil {
			t.Errorf("%s reply to an unknown prompt succeeded", name)
		}
	}
	if _, err := p.ListQuestions(ctx, "thr"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.RefreshPermissions(ctx, "thr"); err != nil {
		t.Fatal(err)
	}
	if !p.ReplyOutcomes(ctx, "thr") {
		t.Fatal("reply outcomes")
	}
	if err := p.Abort(ctx, platforms.AbortRequest{SessionID: "thr"}); err != nil {
		t.Fatalf("abort while idle = %v", err)
	}
	forked, err := p.ForkSession(ctx, platforms.ForkSessionRequest{SessionID: "thr"})
	if err != nil || forked.ID != "thr-fork" {
		t.Fatalf("fork = %+v %v", forked, err)
	}
	ev := &idles{ch: make(chan string, 8)}
	p.SetSessionEventSink(ev.sink())
	go func() { _ = p.WatchEvents(ctx) }()
	// Shell, compaction and a slash command each run a turn that settles.
	for _, op := range []struct {
		name string
		run  func() error
	}{
		{"shell", func() error { return p.RunShell(ctx, platforms.RunShellRequest{SessionID: "thr", Command: "ls"}) }},
		{"compact", func() error { return p.Compact(ctx, platforms.CompactRequest{SessionID: "thr"}) }},
		{"command", func() error {
			return p.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: "thr", Command: "compact"})
		}},
	} {
		if err := op.run(); err != nil {
			t.Fatalf("%s = %v", op.name, err)
		}
		if id := ev.wait(t); id != "thr" {
			t.Fatalf("%s idle = %q", op.name, id)
		}
	}
	if err := p.MoveSession(ctx, platforms.MoveSessionRequest{SessionID: "thr", Directory: "/x"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Errorf("move = %v", err)
	}
	// A browser stream attaches with the session's snapshot.
	events, stop := context.WithTimeout(ctx, 300*time.Millisecond)
	defer stop()
	var w strings.Builder
	_ = p.ProxyEvents(events, "thr", &w, func() {})
	p.Close()
	if err := p.WatchEvents(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("watch after close = %v", err)
	}
}
