package remote

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider"
	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/platformtest"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

// startClaudeRemote serves a real Claude provider, driving the fake claude
// executable, as the remote's claudecode platform, and returns the hub's
// registered r-<id>:claudecode adapter plus the remote's working directory.
func startClaudeRemote(t *testing.T, fixture string) (platforms.Platform, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "clr-") // short: unix socket path limit
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	// External transcripts can be continued on their owning machine.
	if err := os.CopyFS(filepath.Join(dir, "config", "projects"), os.DirFS(filepath.Join("..", "claudeprovider", "testdata", "projects"))); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "config", "projects", "-work-repo", "11111111-1111-4111-8111-111111111111.jsonl")
	data, err := os.ReadFile(transcript)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, bytes.ReplaceAll(data, []byte("/work/repo"), []byte(dir)), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_CLAUDE_FIXTURE", fixture)
	t.Setenv("FAKE_CLAUDE_STDIN", filepath.Join(dir, "stdin.jsonl"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	opts := claudeprovider.Options{IndexPath: filepath.Join(dir, "claude-index.db"), ConfigDir: filepath.Join(dir, "config"),
		CLI: claudetest.Binary(t), Sweep: time.Hour, IdleTimeout: 50 * time.Millisecond}
	sup, err := claudeprovider.NewSupervisor(ctx, opts, filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "s")
	go func() { _ = sup.Serve(ctx, socket) }()
	var adapter *provider.Adapter
	for deadline := time.Now().Add(5 * time.Second); adapter == nil; time.Sleep(10 * time.Millisecond) {
		if adapter, err = provider.New(ctx, socket); err != nil && time.Now().After(deadline) {
			t.Fatal(err)
		}
	}
	t.Cleanup(adapter.Close)
	rules, err := provider.OpenRules(filepath.Join(dir, "rules.json"))
	if err != nil {
		t.Fatal(err)
	}
	adapter.SetRules(rules)
	// The remote's own server runs the headless watcher (watchProviderPrompts).
	go func() { _ = adapter.WatchEvents(ctx) }()

	reg := platforms.NewRegistry()
	reg.Register(providerOwner{Adapter: adapter})
	conn := NewRemoteConn(startRealRemote(t, "tok", "box", reg), "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	mr := &managedRemote{localID: 1, conn: conn}
	m.remotes[1] = mr
	m.connectAndRegisterTest(t, mr)
	claude, ok := m.registry.Get("r-box:claudecode")
	if !ok {
		t.Fatalf("hub registered %v", m.registry.Platforms())
	}
	return claude, dir
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// streamEvents proxies the remote session's events to the hub over gRPC.
func streamEvents(t *testing.T, p platforms.Platform, id string) *syncBuffer {
	t.Helper()
	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	go func() { _ = p.ProxyEvents(ctx, id, out, func() {}) }()
	return out
}

// The hub creates, lists and drives a Claude session that runs on a remote.
func TestHubDrivesRemoteClaudeSession(t *testing.T) {
	claude, dir := startClaudeRemote(t, claudetest.Fixture("plain.jsonl"))
	if !claude.Capabilities().Composer {
		t.Fatalf("capabilities = %+v", claude.Capabilities())
	}
	created, err := claude.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: dir, Title: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := claude.Sessions(t.Context(), dir, 0)
	if err != nil || !slices.ContainsFunc(sessions, func(s db.Session) bool {
		return s.ID == created.ID && s.Platform == "r-box:claudecode"
	}) {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	if !claude.Owns(t.Context(), created.ID) {
		t.Fatal("hub adapter does not own the remote session")
	}
	events := streamEvents(t, claude, created.ID)
	if err := claude.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, waitTimeout, "turn reply on the hub", func() bool { return strings.Contains(events.String(), "session.idle") })
	if !strings.Contains(events.String(), "message.part.updated") {
		t.Fatalf("no streamed reply:\n%s", events.String())
	}
	// The fake writes no transcript; the detail read still crosses the seam.
	if detail, err := claude.Session(t.Context(), created.ID, 10, 0); err != nil || detail.Session.ID != created.ID || detail.Session.Platform != "r-box:claudecode" || !detail.Session.LiveConnection {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
}

// Continuing an external session stays on the remote, including its events.
func TestHubContinuesRemoteTerminalSession(t *testing.T) {
	claude, _ := startClaudeRemote(t, claudetest.Fixture("plain.jsonl"))
	const terminal = "11111111-1111-4111-8111-111111111111"
	var detail *platforms.SessionDetail
	testutil.WaitFor(t, waitTimeout, "terminal session indexed", func() bool {
		d, err := claude.Session(t.Context(), terminal, 10, 0)
		detail = d
		return err == nil && d.Session != nil
	})
	if !detail.Session.LiveConnection {
		t.Fatalf("terminal session detail = %+v, want live connection", detail.Session)
	}
	lc, err := claude.(platforms.LifecycleReader).SessionLifecycle(t.Context(), terminal)
	if err != nil || lc.ReadOnly {
		t.Fatalf("lifecycle = %+v, %v; want writable", lc, err)
	}
	events := streamEvents(t, claude, terminal)
	if err := claude.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: terminal, Message: "hi"}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, waitTimeout, "external session reply", func() bool { return strings.Contains(events.String(), "session.idle") })
}

// A permission the remote cannot decide is shown and answered on the hub.
func TestHubAnswersRemoteClaudePermission(t *testing.T) {
	claude, dir := startClaudeRemote(t, claudetest.Fixture("permission.jsonl"))
	created, err := claude.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: dir, Title: "remote"})
	if err != nil {
		t.Fatal(err)
	}
	events := streamEvents(t, claude, created.ID)
	if err := claude.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: created.ID, Message: "go"}); err != nil {
		t.Fatal(err)
	}
	var pending []platforms.LivePrompt
	testutil.WaitFor(t, waitTimeout, "pending prompt on the hub", func() bool {
		pending, _ = claude.ListPermissions(t.Context(), created.ID)
		return len(pending) == 1 && strings.Contains(events.String(), "permission.asked")
	})
	if pending[0]["permission"] != "bash" || !strings.Contains(events.String(), "permission.asked") {
		t.Fatalf("prompt = %+v\nevents:\n%s", pending[0], events.String())
	}
	if err := claude.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: created.ID,
		PermissionID: pending[0]["id"].(string), Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	testutil.WaitFor(t, waitTimeout, "turn finished after the hub's reply", func() bool { return strings.Contains(events.String(), "session.idle") })
	if log, _ := os.ReadFile(os.Getenv("FAKE_CLAUDE_STDIN")); !strings.Contains(string(log), `"behavior":"allow"`) {
		t.Fatalf("remote claude was not answered: %s", log)
	}
	if left, _ := claude.ListPermissions(t.Context(), created.ID); len(left) != 0 {
		t.Fatalf("prompt still pending: %+v", left)
	}
}

// providerOwner registers the owner's socket adapter for protocol tests. The
// remote RPCs under test forward session pages, which the double answers from
// the supervisor; a production owner reads them from statev2.
type providerOwner struct {
	*provider.Adapter
	platformtest.NoBrowsing
}

func (p providerOwner) Session(ctx context.Context, id string, limit, offset int) (*platforms.SessionDetail, error) {
	return p.RuntimeDetail(ctx, id, limit, offset)
}
func (p providerOwner) Owns(_ context.Context, id string) bool { return p.Knows(id) }
