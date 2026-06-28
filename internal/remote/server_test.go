package remote

import (
	"context"
	"io"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/gitinfo"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"github.com/NoUseFreak/ocman/internal/worktree"
)

// --- fakes ---

type fakePlatform struct {
	id       string
	sessions []db.Session
	sent     *platforms.SendMessageRequest
	events   []string
}

func (f *fakePlatform) ID() platforms.ID                 { return platforms.ID(f.id) }
func (f *fakePlatform) DisplayName() string              { return "Fake" }
func (f *fakePlatform) Available(context.Context) bool   { return true }
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
func (f *fakePlatform) Abort(context.Context, platforms.AbortRequest) error           { return nil }
func (f *fakePlatform) RenameSession(context.Context, platforms.RenameSessionRequest) error {
	return nil
}
func (f *fakePlatform) Compact(context.Context, platforms.CompactRequest) error { return nil }
func (f *fakePlatform) CreateSession(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
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
func (localStubHost) GitInfo(context.Context, []string) (map[string]gitinfo.Info, error) {
	return map[string]gitinfo.Info{}, nil
}
func (localStubHost) GitDiff(context.Context, string, hostsvc.GitDiffOptions) (*gitinfo.Diff, error) {
	return &gitinfo.Diff{}, nil
}
func (localStubHost) ListWorktrees(context.Context, string) ([]worktree.Entry, error) {
	return nil, nil
}
func (localStubHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) { return "main", nil }
func (localStubHost) CreateWorktreeSession(context.Context, hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	return &hostsvc.WorktreeSessionResult{WorktreePath: "/wt", Branch: "b"}, nil
}
func (localStubHost) RemoveWorktree(context.Context, hostsvc.RemoveWorktreeRequest) error { return nil }
func (localStubHost) LaunchTmux(context.Context, hostsvc.LaunchTmuxRequest) (*hostsvc.LaunchTmuxResult, error) {
	return &hostsvc.LaunchTmuxResult{Session: "sess"}, nil
}
func (localStubHost) TmuxSessions(context.Context) ([]hostsvc.TmuxSession, error) { return nil, nil }
func (localStubHost) Projects(context.Context) ([]db.ProjectStats, error) {
	return []db.ProjectStats{{Directory: "/home/u/app"}}, nil
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
		if err == io.EOF {
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
