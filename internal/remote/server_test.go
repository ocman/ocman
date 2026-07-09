package remote

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

// --- fakes ---

type fakePlatform struct {
	id        string
	sessions  []db.Session
	sent      *platforms.SendMessageRequest
	events    []string
	createErr error

	permRules  []platforms.PermissionRule
	setPermReq *platforms.SetPermissionRulesRequest
}

func (f *fakePlatform) ID() platforms.ID               { return platforms.ID(f.id) }
func (f *fakePlatform) DisplayName() string            { return "Fake" }
func (f *fakePlatform) Available(context.Context) bool { return true }
func (f *fakePlatform) Capabilities() platforms.Capabilities {
	return platforms.Capabilities{Composer: true, Events: true}
}
func (f *fakePlatform) Sessions(_ context.Context, _ string, _ int64) ([]db.Session, error) {
	return f.sessions, nil
}
func (f *fakePlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Platform: f.id}}, nil
}
func (f *fakePlatform) Owns(_ context.Context, id string) bool {
	for _, s := range f.sessions {
		if s.ID == id {
			return true
		}
	}
	return false
}
func (f *fakePlatform) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return nil, nil
}
func (f *fakePlatform) SessionChanges(context.Context, string) (*platforms.SessionChanges, error) {
	return &platforms.SessionChanges{}, nil
}
func (f *fakePlatform) SessionInfo(context.Context, string) (*platforms.SessionInfo, error) {
	return &platforms.SessionInfo{}, nil
}
func (f *fakePlatform) LiveStatus(string) *platforms.LiveState { return nil }
func (f *fakePlatform) AgentCatalog(context.Context, string) ([]platforms.AgentCatalogEntry, error) {
	return nil, nil
}
func (f *fakePlatform) SlashCommands(context.Context, string) ([]platforms.SlashCommandEntry, error) {
	return nil, nil
}
func (f *fakePlatform) SessionModels(context.Context, string) (*platforms.SessionModelsResponse, error) {
	return &platforms.SessionModelsResponse{}, nil
}
func (f *fakePlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}
func (f *fakePlatform) ListQuestions(context.Context, string) ([]platforms.LivePrompt, error) {
	return nil, nil
}
func (f *fakePlatform) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	f.sent = &req
	return nil
}
func (f *fakePlatform) ExecuteCommand(context.Context, platforms.ExecuteCommandRequest) error {
	return nil
}
func (f *fakePlatform) RunShell(context.Context, platforms.RunShellRequest) error { return nil }
func (f *fakePlatform) RespondPermission(context.Context, platforms.RespondPermissionRequest) error {
	return nil
}
func (f *fakePlatform) RespondQuestion(context.Context, platforms.RespondQuestionRequest) error {
	return nil
}
func (f *fakePlatform) RejectQuestion(context.Context, platforms.RejectQuestionRequest) error {
	return nil
}
func (f *fakePlatform) Abort(context.Context, platforms.AbortRequest) error { return nil }
func (f *fakePlatform) RenameSession(context.Context, platforms.RenameSessionRequest) error {
	return nil
}
func (f *fakePlatform) PermissionRules(context.Context, string) ([]platforms.PermissionRule, error) {
	return f.permRules, nil
}
func (f *fakePlatform) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	f.setPermReq = &req
	return nil
}
func (f *fakePlatform) Compact(context.Context, platforms.CompactRequest) error { return nil }
func (f *fakePlatform) CreateSession(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &platforms.CreateSessionResponse{ID: "new-sess"}, nil
}
func (f *fakePlatform) ProxyEvents(_ context.Context, _ string, w io.Writer, flush func()) error {
	for _, e := range f.events {
		if _, err := w.Write([]byte(e)); err != nil {
			return err
		}
		flush()
	}
	return nil
}

// localStubHost is a minimal hostsvc.Host for server tests. It returns
// empty results for everything except Projects/Capabilities.
type localStubHost struct{}

func (localStubHost) RemoteID() string { return "local" }
func (localStubHost) Capabilities() hostsvc.HostCaps {
	return hostsvc.HostCaps{GitDiff: true, Worktrees: true, Tmux: true, Projects: true, Whisper: true}
}
func (localStubHost) GitInfo(context.Context, []string) (map[string]git.Info, error) {
	return map[string]git.Info{}, nil
}
func (localStubHost) GitDiff(context.Context, string, hostsvc.GitDiffOptions) (*git.Diff, error) {
	return &git.Diff{}, nil
}
func (localStubHost) GitBranches(context.Context, string) ([]string, error) {
	return []string{"main"}, nil
}
func (localStubHost) GitCheckout(context.Context, string, string) error { return nil }
func (localStubHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	return nil, nil
}
func (localStubHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) {
	return "main", nil
}
func (localStubHost) CreateWorktreeSession(context.Context, hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	return &hostsvc.WorktreeSessionResult{WorktreePath: "/wt", Branch: "b"}, nil
}
func (localStubHost) RemoveWorktree(context.Context, hostsvc.RemoveWorktreeRequest) error { return nil }
func (localStubHost) LaunchTmux(context.Context, hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	return &hostsvc.LaunchTmuxResult{Session: "sess"}, nil
}
func (localStubHost) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Port: "1234", RepoRoot: "/repo", TmuxSession: "sess"}, nil
}
func (localStubHost) TmuxSessions(context.Context) ([]hostsvc.TmuxSession, error) { return nil, nil }
func (localStubHost) Projects(context.Context) ([]db.ProjectStats, error) {
	return []db.ProjectStats{{Directory: "/home/u/app"}}, nil
}
func (localStubHost) TermWindows(context.Context, string) ([]hostsvc.TermWindow, error) {
	return []hostsvc.TermWindow{{Name: "ocman-abc-1", Title: "vim"}}, nil
}
func (localStubHost) TermCreateWindow(context.Context, string) (string, error) {
	return "ocman-abc-2", nil
}
func (localStubHost) TermKillWindow(context.Context, string, string) error { return nil }

// TermAttach echoes each viewer frame's data back and stops on a resize,
// so the bidi roundtrip test can observe both directions deterministically.
func (localStubHost) TermAttach(_ context.Context, _ hostsvc.TermAttachRequest, conn hostsvc.TermConn) error {
	for {
		frame, err := conn.Recv()
		if err != nil {
			return nil
		}
		if frame.Resize != nil {
			return nil
		}
		if err := conn.Write(frame.Data); err != nil {
			return nil
		}
	}
}

func startTestServer(t *testing.T, token string, srv *Server) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	unary, stream := NewAuthInterceptors(token)
	gs := grpc.NewServer(grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream))
	pb.RegisterOcmanServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bearerCreds{token: token, requireTLS: false}),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestServer_HelloAndSessionsRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	fp := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "s1", Platform: "opencode", Title: "Hi"}}}
	reg.Register(fp)
	srv := NewServer(reg, localStubHost{}, "inst-1", "v-test")

	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)
	ctx := context.Background()

	hello, err := client.Hello(ctx, &pb.HelloReq{ProtocolVersion: ProtocolVersion})
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if hello.InstanceId != "inst-1" || hello.ProtocolVersion != ProtocolVersion {
		t.Fatalf("unexpected hello: %+v", hello)
	}

	resp, err := client.Sessions(ctx, &pb.SessionsReq{Platform: "opencode"})
	if err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	var got []db.Session
	if err := unmarshalJSON(resp.Payload, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "s1" || got[0].Title != "Hi" {
		t.Fatalf("session round-trip mismatch: %+v", got)
	}
}

func TestServer_CreateSessionMapsUnreachableToUnavailable(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", createErr: platforms.ErrPlatformUnreachable})
	srv := NewServer(reg, localStubHost{}, "iid", "test")
	b, err := marshalJSON(platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	_, err = srv.CreateSession(context.Background(), &pb.PlatformJsonReq{Platform: "opencode", Payload: b})
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("CreateSession error code = %v, want Unavailable (err=%v)", status.Code(err), err)
	}
}

// TestServer_MutationsUseSharedSessionService proves gRPC-executed
// mutations run through the shared sessionsvc: hooks fire on the
// remote-executed path and validation errors map to InvalidArgument.
func TestServer_MutationsUseSharedSessionService(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	var replied []string
	created := 0
	svc := sessionsvc.New(reg, sessionsvc.Hooks{
		PermissionReplied: func(sessionID, permissionID string) {
			replied = append(replied, sessionID+"|"+permissionID)
		},
		SessionCreated: func() { created++ },
	})
	srv := NewServer(reg, localStubHost{}, "i", "v").UseSessions(svc)
	ctx := context.Background()

	b, err := marshalJSON(platforms.RespondPermissionRequest{SessionID: "s1", PermissionID: "p1", Reply: "once"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := srv.RespondPermission(ctx, &pb.PlatformJsonReq{Platform: "opencode", Payload: b}); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	if len(replied) != 1 || replied[0] != "s1|p1" {
		t.Fatalf("expected PermissionReplied hook s1|p1, got %v", replied)
	}

	b, err = marshalJSON(platforms.RespondPermissionRequest{SessionID: "s1", PermissionID: "p1", Reply: "maybe"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, err = srv.RespondPermission(ctx, &pb.PlatformJsonReq{Platform: "opencode", Payload: b})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("invalid reply error code = %v, want InvalidArgument (err=%v)", status.Code(err), err)
	}
	if len(replied) != 1 {
		t.Fatal("hook fired for an invalid reply")
	}

	b, err = marshalJSON(platforms.CreateSessionRequest{Directory: "/repo"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := srv.CreateSession(ctx, &pb.PlatformJsonReq{Platform: "opencode", Payload: b}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if created != 1 {
		t.Fatalf("expected SessionCreated hook once, got %d", created)
	}
}

func TestRemotePlatformErrorRestoresUnreachableSentinel(t *testing.T) {
	err := remotePlatformError(status.Error(codes.Unavailable, "no running platform instance"))
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("errors.Is(unreachable) = false for %v", err)
	}
}

func TestServer_SendMessageRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	fp := &fakePlatform{id: "opencode"}
	reg.Register(fp)
	srv := NewServer(reg, localStubHost{}, "i", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	payload, _ := marshalJSON(platforms.SendMessageRequest{SessionID: "s1", Message: "hello there"})
	if _, err := client.SendMessage(context.Background(), &pb.PlatformJsonReq{Platform: "opencode", Payload: payload}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if fp.sent == nil || fp.sent.Message != "hello there" || fp.sent.SessionID != "s1" {
		t.Fatalf("send not delivered: %+v", fp.sent)
	}
}

func TestServer_StreamEventsRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	fp := &fakePlatform{id: "opencode", events: []string{"event: a\n\n", "event: b\n\n"}}
	reg.Register(fp)
	srv := NewServer(reg, localStubHost{}, "i", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	stream, err := client.StreamEvents(context.Background(), &pb.SessionRef{Platform: "opencode", SessionId: "s1"})
	if err != nil {
		t.Fatalf("StreamEvents: %v", err)
	}
	var chunks []string
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("recv: %v", err)
		}
		chunks = append(chunks, string(chunk.Data))
	}
	if len(chunks) != 2 || chunks[0] != "event: a\n\n" || chunks[1] != "event: b\n\n" {
		t.Fatalf("event stream mismatch: %v", chunks)
	}
}

func TestServer_TermWindowsRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	srv := NewServer(reg, localStubHost{}, "i", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	b, _ := marshalJSON(map[string]any{"dir": "/home/u/app"})
	resp, err := client.TermWindows(context.Background(), &pb.JsonReq{Payload: b})
	if err != nil {
		t.Fatalf("TermWindows: %v", err)
	}
	var wins []hostsvc.TermWindow
	if err := unmarshalJSON(resp.Payload, &wins); err != nil {
		t.Fatalf("unmarshal windows: %v", err)
	}
	if len(wins) != 1 || wins[0].Name != "ocman-abc-1" || wins[0].Title != "vim" {
		t.Fatalf("windows mismatch: %+v", wins)
	}

	cresp, err := client.TermCreateWindow(context.Background(), &pb.JsonReq{Payload: b})
	if err != nil {
		t.Fatalf("TermCreateWindow: %v", err)
	}
	var created struct {
		Window string `json:"window"`
	}
	_ = unmarshalJSON(cresp.Payload, &created)
	if created.Window != "ocman-abc-2" {
		t.Fatalf("created window = %q", created.Window)
	}

	killReq, _ := marshalJSON(map[string]any{"dir": "/home/u/app", "window": "ocman-abc-1"})
	if _, err := client.TermKillWindow(context.Background(), &pb.JsonReq{Payload: killReq}); err != nil {
		t.Fatalf("TermKillWindow: %v", err)
	}
}

func TestServer_TerminalStreamRoundTrip(t *testing.T) {
	reg := platforms.NewRegistry()
	srv := NewServer(reg, localStubHost{}, "i", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	stream, err := client.TerminalStream(context.Background())
	if err != nil {
		t.Fatalf("TerminalStream: %v", err)
	}
	// First frame must select the window.
	if err := stream.Send(&pb.TermClientMsg{Open: &pb.TermOpen{Dir: "/home/u/app", Window: "ocman-abc-1"}}); err != nil {
		t.Fatalf("send open: %v", err)
	}
	// Keystrokes are echoed back by the stub's TermAttach.
	if err := stream.Send(&pb.TermClientMsg{Data: []byte("ls\n")}); err != nil {
		t.Fatalf("send data: %v", err)
	}
	out, err := stream.Recv()
	if err != nil {
		t.Fatalf("recv echo: %v", err)
	}
	if string(out.Data) != "ls\n" {
		t.Fatalf("echo mismatch: %q", out.Data)
	}
	// A resize ends the stub attach, closing the stream (EOF).
	if err := stream.Send(&pb.TermClientMsg{Resize: &pb.TermResize{Cols: 80, Rows: 24}}); err != nil {
		t.Fatalf("send resize: %v", err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF after resize, got %v", err)
	}
}

func TestServer_TerminalStreamRequiresOpenFirst(t *testing.T) {
	reg := platforms.NewRegistry()
	srv := NewServer(reg, localStubHost{}, "i", "v")
	conn := startTestServer(t, "tok", srv)
	client := pb.NewOcmanClient(conn)

	stream, err := client.TerminalStream(context.Background())
	if err != nil {
		t.Fatalf("TerminalStream: %v", err)
	}
	// First frame carries data, not open -> InvalidArgument.
	if err := stream.Send(&pb.TermClientMsg{Data: []byte("x")}); err != nil {
		t.Fatalf("send: %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
}

func TestServer_RejectsBadToken(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	srv := NewServer(reg, localStubHost{}, "i", "v")

	lis := bufconn.Listen(1 << 20)
	unary, stream := NewAuthInterceptors("correct-token")
	gs := grpc.NewServer(grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream))
	pb.RegisterOcmanServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(bearerCreds{token: "WRONG", requireTLS: false}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	_, err = pb.NewOcmanClient(conn).Hello(context.Background(), &pb.HelloReq{})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expected Unauthenticated, got %v", err)
	}
}

// scriptedTermStream is a fake TerminalStreamServer that hands out a
// fixed sequence of client messages, then a terminal error. It embeds
// grpc.ServerStream (nil) only to satisfy the fat interface; the test
// exercises streamTermConn.Recv directly, which touches only Recv/Send,
// so the embedded methods are never called.
type scriptedTermStream struct {
	grpc.ServerStream
	msgs []*pb.TermClientMsg
	i    int
	end  error
}

func (s *scriptedTermStream) Recv() (*pb.TermClientMsg, error) {
	if s.i >= len(s.msgs) {
		return nil, s.end
	}
	m := s.msgs[s.i]
	s.i++
	return m, nil
}

func (s *scriptedTermStream) Send(*pb.TermServerMsg) error { return nil }

// TestStreamTermConn_RecvBranches drives streamTermConn.Recv over a
// scripted, deterministic frame sequence so every branch of its loop
// (resize, data, skip-empty, error) is exercised on every run — closing
// the coverage-jitter gap left by the network-timing-dependent
// TerminalStreamRoundTrip test.
func TestStreamTermConn_RecvBranches(t *testing.T) {
	sentinel := errors.New("stream closed")
	conn := &streamTermConn{stream: &scriptedTermStream{
		msgs: []*pb.TermClientMsg{
			{Resize: &pb.TermResize{Cols: 80, Rows: 24}},
			{Data: []byte("ls\n")},
			{}, // empty/open-only frame: must be skipped, not returned
			{Data: []byte("q")},
		},
		end: sentinel,
	}}

	// 1) resize frame -> resize TermFrame
	f, err := conn.Recv()
	if err != nil {
		t.Fatalf("resize recv: %v", err)
	}
	if f.Resize == nil || f.Resize.Cols != 80 || f.Resize.Rows != 24 {
		t.Fatalf("resize mismatch: %#v", f)
	}

	// 2) data frame -> data TermFrame
	f, err = conn.Recv()
	if err != nil {
		t.Fatalf("data recv: %v", err)
	}
	if string(f.Data) != "ls\n" {
		t.Fatalf("data mismatch: %q", f.Data)
	}

	// 3) empty frame is skipped; the loop continues to the next data
	// frame in the same Recv call.
	f, err = conn.Recv()
	if err != nil {
		t.Fatalf("skip-empty recv: %v", err)
	}
	if string(f.Data) != "q" {
		t.Fatalf("expected empty frame skipped then 'q', got %q", f.Data)
	}

	// 4) terminal error propagates.
	if _, err := conn.Recv(); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
