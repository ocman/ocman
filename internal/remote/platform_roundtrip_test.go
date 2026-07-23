package remote

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/workflows"
)

// connectedPair spins an in-process remote server over a fakePlatform +
// localStubHost and returns a connected RemoteConn dialing it. Used to
// exercise every remotePlatform / remoteHost method against a real wire.
func connectedPair(t *testing.T, fp *fakePlatform) *RemoteConn {
	t.Helper()
	reg := platforms.NewRegistry()
	reg.Register(fp)
	addr := startRealRemote(t, "tok", "rid", reg)
	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(context.Background()); err != nil {
		t.Fatalf("connect: %v", err)
	}
	return conn
}

// TestRemotePlatform_AllReadMethods drives every read-only Platform RPC
// through the client adapter and the server shim, covering both ends.
func TestRemotePlatform_AllReadMethods(t *testing.T) {
	fp := &fakePlatform{id: "opencode", sessions: []db.Session(nil)}
	conn := connectedPair(t, fp)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	ctx := context.Background()

	if rp.DisplayName() != "OpenCode" {
		t.Errorf("DisplayName = %q", rp.DisplayName())
	}
	if !rp.Available(ctx) {
		t.Error("Available should be true while connected")
	}
	if string(rp.ID()) != "r-rid:opencode" {
		t.Errorf("ID = %q", rp.ID())
	}
	// Capabilities is fetched + cached over gRPC.
	if caps := rp.Capabilities(); !caps.Composer {
		t.Errorf("Capabilities = %+v", caps)
	}
	// Second call hits the cache (still correct).
	if caps := rp.Capabilities(); !caps.Composer {
		t.Errorf("cached Capabilities = %+v", caps)
	}

	if _, err := rp.SessionsInactiveBefore(ctx, 0); err != nil {
		t.Errorf("SessionsInactiveBefore: %v", err)
	}
	if _, err := rp.SessionChanges(ctx, "s1"); err != nil {
		t.Errorf("SessionChanges: %v", err)
	}
	if _, err := rp.SessionInfo(ctx, "s1"); err != nil {
		t.Errorf("SessionInfo: %v", err)
	}
	if _, err := rp.AgentCatalog(ctx, "s1"); err != nil {
		t.Errorf("AgentCatalog: %v", err)
	}
	if _, err := rp.SlashCommands(ctx, "s1"); err != nil {
		t.Errorf("SlashCommands: %v", err)
	}
	if _, err := rp.SessionModels(ctx, "s1"); err != nil {
		t.Errorf("SessionModels: %v", err)
	}
	if _, err := rp.ListPermissions(ctx, "s1"); err != nil {
		t.Errorf("ListPermissions: %v", err)
	}
	if _, err := rp.ListQuestions(ctx, "s1"); err != nil {
		t.Errorf("ListQuestions: %v", err)
	}
	if _, err := rp.PermissionRules(ctx, "s1"); err != nil {
		t.Errorf("PermissionRules: %v", err)
	}
	detail, err := rp.Session(ctx, "s1", 10, 0)
	if err != nil {
		t.Errorf("Session: %v", err)
	} else if detail.Session == nil || !detail.Session.LiveConnection {
		t.Errorf("Session LiveConnection = false, want true for connected remote")
	}
	if rp.LiveStatus("s1") != nil {
		t.Error("LiveStatus should be nil for a remote adapter")
	}
}

// TestServer_OwnsRPC covers the server-side Owns shim (the client adapter
// is cache-only and never calls it, so exercise the RPC directly).
func TestServer_OwnsRPC(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1", Platform: "opencode"}}})
	srv := NewServer(reg, localStubHost{}, "rid", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	got, err := client.Owns(context.Background(), &pb.SessionRef{Platform: "opencode", SessionId: "s1"})
	if err != nil {
		t.Fatalf("Owns: %v", err)
	}
	if !got.Owns {
		t.Error("expected Owns true for s1")
	}
	miss, _ := client.Owns(context.Background(), &pb.SessionRef{Platform: "opencode", SessionId: "nope"})
	if miss.Owns {
		t.Error("expected Owns false for unknown session")
	}
}

// TestRemotePlatform_AllMutations drives every mutating Platform RPC.
func TestRemotePlatform_AllMutations(t *testing.T) {
	fp := &fakePlatform{id: "opencode"}
	conn := connectedPair(t, fp)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	ctx := context.Background()

	checks := []struct {
		name string
		fn   func() error
	}{
		{"ExecuteCommand", func() error {
			return rp.ExecuteCommand(ctx, platforms.ExecuteCommandRequest{SessionID: "s", Command: "/init"})
		}},
		{"RunShell", func() error { return rp.RunShell(ctx, platforms.RunShellRequest{SessionID: "s", Command: "ls"}) }},
		{"RespondPermission", func() error {
			return rp.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p1", Reply: "once"})
		}},
		{"RespondQuestion", func() error {
			return rp.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: "s", RequestID: "q1"})
		}},
		{"RejectQuestion", func() error {
			return rp.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q1"})
		}},
		{"Abort", func() error { return rp.Abort(ctx, platforms.AbortRequest{SessionID: "s"}) }},
		{"RenameSession", func() error { return rp.RenameSession(ctx, platforms.RenameSessionRequest{SessionID: "s", Title: "t"}) }},
		{"Compact", func() error {
			return rp.Compact(ctx, platforms.CompactRequest{SessionID: "s", ProviderID: "anthropic", ModelID: "m"})
		}},
		{"SetPermissionRules", func() error {
			return rp.SetPermissionRules(ctx, platforms.SetPermissionRulesRequest{
				SessionID: "s",
				Rules:     []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}},
			})
		}},
		{"MoveSession", func() error {
			return rp.MoveSession(ctx, platforms.MoveSessionRequest{SessionID: "s", Directory: "/tmp/dst"})
		}},
	}
	for _, c := range checks {
		if err := c.fn(); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}

	// ForkSession returns a value; assert the roundtripped ID.
	resp, err := rp.ForkSession(ctx, platforms.ForkSessionRequest{SessionID: "s"})
	if err != nil {
		t.Errorf("ForkSession: %v", err)
	} else if resp.ID != "forked-sess" {
		t.Errorf("ForkSession ID = %q, want forked-sess", resp.ID)
	}
}

// TestRemotePlatform_ProxyEvents covers the gRPC->writer event tunnel.
func TestRemotePlatform_ProxyEvents(t *testing.T) {
	fp := &fakePlatform{id: "opencode", events: []string{"event: x\n\n"}}
	conn := connectedPair(t, fp)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })

	var buf bytes.Buffer
	if err := rp.ProxyEvents(context.Background(), "s1", &buf, func() {}); err != nil {
		t.Fatalf("ProxyEvents: %v", err)
	}
	if buf.String() != "event: x\n\n" {
		t.Errorf("ProxyEvents output = %q", buf.String())
	}
}

// TestRemoteHost_AllMethods drives every remoteHost (Host) RPC.
func TestRemoteHost_AllMethods(t *testing.T) {
	fp := &fakePlatform{id: "opencode"}
	conn := connectedPair(t, fp)
	rh := newRemoteHost(conn)
	ctx := context.Background()

	if rh.RemoteID() != "rid" {
		t.Errorf("RemoteID = %q", rh.RemoteID())
	}
	if caps := rh.Capabilities(); !caps.GitDiff {
		t.Errorf("Capabilities = %+v", caps)
	}
	if _, err := rh.GitInfo(ctx, []string{"/x"}); err != nil {
		t.Errorf("GitInfo: %v", err)
	}
	if _, err := rh.GitDiff(ctx, "/x", hostsvc.GitDiffOptions{Force: true}); err != nil {
		t.Errorf("GitDiff: %v", err)
	}
	if _, err := rh.ListWorktrees(ctx, "/x"); err != nil {
		t.Errorf("ListWorktrees: %v", err)
	}
	if ref, err := rh.WorktreeDefaultBaseRef(ctx, "/x"); err != nil || ref != "main" {
		t.Errorf("WorktreeDefaultBaseRef = %q, %v", ref, err)
	}
	if _, err := rh.LaunchTmux(ctx, hostsvc.LaunchTmuxRequest{Directory: "/x"}); err != nil {
		t.Errorf("LaunchTmux: %v", err)
	}
	if res, err := rh.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: "/x"}); err != nil || res.Port() != "1234" {
		t.Errorf("EnsureProjectOpencode = %+v, %v", res, err)
	}
	// RestartProjectOpencode marshals req+result across the same gRPC seam;
	// localStubHost returns a distinct endpoint (:5678) + Launched=true so
	// the roundtrip is observable, not aliased to the ensure result.
	if res, err := rh.RestartProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: "/x"}); err != nil || res.Port() != "5678" || !res.Launched {
		t.Errorf("RestartProjectOpencode = %+v, %v", res, err)
	}
	if _, err := rh.TmuxSessions(ctx); err != nil {
		t.Errorf("TmuxSessions: %v", err)
	}
	if got := rh.DaguStatus(ctx); got.Status != dagu.Compatible || got.Version != "2.1.0" {
		t.Errorf("DaguStatus: %+v", got)
	}
	definition := workflows.Definition{ID: "release", Nodes: []workflows.Node{{ID: "build", Name: "Build", Type: "command", Command: []string{"true"}}}}
	if run, err := rh.StartDaguWorkflow(ctx, definition); err != nil || run.ID != "run-1" {
		t.Errorf("StartDaguWorkflow = %+v, %v", run, err)
	}
	if run, err := rh.GetDaguRun(ctx, "release", "run-1"); err != nil || run.Status != "running" {
		t.Errorf("GetDaguRun = %+v, %v", run, err)
	}
	if err := rh.CancelDaguRun(ctx, "release", "run-1"); err != nil {
		t.Errorf("CancelDaguRun: %v", err)
	}
	if _, err := rh.Projects(ctx); err != nil {
		t.Errorf("Projects: %v", err)
	}
	if _, err := rh.ProjectIdentities(ctx); err != nil {
		t.Errorf("ProjectIdentities: %v", err)
	}
	if wins, err := rh.TermWindows(ctx, "/x"); err != nil || len(wins) != 1 {
		t.Errorf("TermWindows = %+v, %v", wins, err)
	}
	if name, err := rh.TermCreateWindow(ctx, "/x"); err != nil || name != "ocman-abc-2" {
		t.Errorf("TermCreateWindow = %q, %v", name, err)
	}
	if err := rh.TermKillWindow(ctx, "/x", "ocman-abc-1"); err != nil {
		t.Errorf("TermKillWindow: %v", err)
	}

	// TermAttach: drive one keystroke then a resize. The stub host echoes
	// data and stops on the resize, closing the stream; the forward loop
	// (conn.Recv -> stream.Send) exits cleanly with no error. The echo
	// direction itself is asserted deterministically off the raw stream
	// in TestServer_TerminalStreamRoundTrip.
	fc := &fakeTermConn{in: []hostsvc.TermFrame{
		{Data: []byte("echo hi\n")},
		{Resize: &hostsvc.TermSize{Cols: 80, Rows: 24}},
	}}
	if err := rh.TermAttach(ctx, hostsvc.TermAttachRequest{Dir: "/x", Window: "ocman-abc-1"}, fc); err != nil {
		t.Errorf("TermAttach: %v", err)
	}
}

// fakeTermConn is an in-memory hostsvc.TermConn: Recv drains a fixed
// frame list then returns io.EOF, Write records PTY output. The list
// ends with a resize, which makes the stub host's TermAttach exit and
// close the stream, so the remoteHost's forward loop stops before Recv
// is called again.
type fakeTermConn struct {
	mu     sync.Mutex
	in     []hostsvc.TermFrame
	outBuf []byte
}

func (c *fakeTermConn) Recv() (hostsvc.TermFrame, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.in) == 0 {
		return hostsvc.TermFrame{}, io.EOF
	}
	f := c.in[0]
	c.in = c.in[1:]
	return f, nil
}

func (c *fakeTermConn) Write(p []byte) error {
	c.mu.Lock()
	c.outBuf = append(c.outBuf, p...)
	c.mu.Unlock()
	return nil
}

func (c *fakeTermConn) Close() error { return nil }

// TestRemotePlatform_OfflineMethodsErr confirms adapter methods return
// ErrRemoteOffline (not a panic) once the conn is closed.
func TestRemotePlatform_OfflineMethodsErr(t *testing.T) {
	fp := &fakePlatform{id: "opencode"}
	conn := connectedPair(t, fp)
	rp := newRemotePlatform(conn, "opencode", func() string { return "Box" })
	conn.Close()

	if _, err := rp.Session(context.Background(), "s1", 0, 0); err == nil {
		t.Error("Session on closed conn should error")
	}
	if err := rp.SendMessage(context.Background(), platforms.SendMessageRequest{SessionID: "s"}); !errors.Is(err, ErrRemoteOffline) {
		t.Errorf("SendMessage on closed conn = %v, want ErrRemoteOffline", err)
	}
	// Owns is cache-only and must never error or dial.
	if rp.Owns(context.Background(), "unknown") {
		t.Error("Owns should be false for an unknown session")
	}
}
