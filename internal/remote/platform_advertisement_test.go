package remote

import (
	"context"
	"net"
	"slices"
	"testing"

	"google.golang.org/grpc"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func TestManagerAdvertisedPlatforms(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode", sessions: []db.Session{{ID: "oc", Platform: "opencode"}}})
	reg.Register(&fakePlatform{id: "claudecode", sessions: []db.Session{{ID: "cc", Platform: "claudecode"}}})
	reg.Register(newRemotePlatform(connectedConn("nested", "nested"), "opencode", nil))
	addr := startRealRemote(t, "tok", "two-platforms", reg)
	conn := NewRemoteConn(addr, "tok")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	mr := &managedRemote{localID: 1, conn: conn}
	m.remotes[1] = mr
	m.connectAndRegisterTest(t, mr)
	var ids []string
	for _, p := range m.registry.Platforms() {
		ids = append(ids, string(p.ID()))
		if _, err := p.Sessions(t.Context(), "", 0); err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(ids, []string{"r-two-platforms:opencode", "r-two-platforms:claudecode"}) {
		t.Fatalf("registered platforms = %v", ids)
	}
	if got := m.sessionCount(1); got != 2 {
		t.Fatalf("sessionCount = %d, want 2", got)
	}
	if got := m.EnabledRemotes(); len(got) != 1 || got[0].Platform != "r-two-platforms:opencode" {
		t.Fatalf("enabled targets = %v", got)
	}
	m.inventory["two-platforms"] = []ProjectIdentity{{Dir: "/remote", Key: "host/repo"}}
	if got := resolveTargets(m, "/local", "https://host/repo", nil); len(got) != 1 || got[0].Platform != "r-two-platforms:opencode" {
		t.Fatalf("project targets = %v", got)
	}
	if got := m.ResolveProjectTargets(db.ProjectStats{Directory: "/local", Platform: "claudecode", UpstreamKeys: []string{"host/repo"}}, nil); len(got) != 1 || got[0].Platform != "r-two-platforms:claudecode" {
		t.Fatalf("Claude project targets = %v", got)
	}
	claude, _ := m.registry.Get("r-two-platforms:claudecode")
	if claude.DisplayName() != "Claude Code" {
		t.Fatalf("display name = %q", claude.DisplayName())
	}
	m.inventory["two-platforms"] = projectIdentities([]db.ProjectStats{{Directory: "/remote", Platform: "claudecode"}})
	if got := m.RemoteProjects(); len(got) != 1 || got[0].Platform != "r-two-platforms:claudecode" {
		t.Fatalf("remote projects = %v", got)
	}
	m.disconnect(1)
	if len(m.registry.Platforms()) != 0 || len(m.router.Remotes()) != 0 {
		t.Fatal("adapters survived disconnect")
	}
}

type legacyHelloServer struct{ *Server }

func (s legacyHelloServer) Hello(ctx context.Context, req *pb.HelloReq) (*pb.HelloResp, error) {
	hello, err := s.Server.Hello(ctx, req)
	hello.Platforms = nil
	return hello, err
}

func TestManagerEmptyAdvertisementDoesNotInventSessionPlatforms(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOcmanServer(server, legacyHelloServer{NewServer(platforms.NewRegistry(), localStubHost{}, "legacy", "old")})
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(server.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr().String(), "")
	t.Cleanup(conn.Close)
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "claudecode")
	mr := &managedRemote{localID: 1, conn: conn}
	m.remotes[1] = mr
	m.connectAndRegisterTest(t, mr)
	registered := m.registry.Platforms()
	if len(registered) != 0 || len(m.router.Remotes()) != 1 {
		t.Fatalf("empty advertisement invented a session platform or lost its host: %v", registered)
	}
	if targets := m.EnabledRemotes(); len(targets) != 0 {
		t.Fatalf("host-only remote became a session creation target: %+v", targets)
	}
	m.unregisterAdapters(mr)
	if len(m.registry.Platforms()) != 0 || len(m.router.Remotes()) != 0 {
		t.Fatal("adapters survived transport teardown")
	}
}

func TestClaudeOnlyRemoteTargets(t *testing.T) {
	conn := connectedConn("claude-only", "host")
	conn.platforms = []string{"claudecode"}
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	m.remotes[1] = &managedRemote{localID: 1, conn: conn, host: newRemoteHost(conn)}
	m.inventory["claude-only"] = []ProjectIdentity{{Dir: "/remote/repo", Key: "host/repo"}}
	if got := m.EnabledRemotes(); len(got) != 1 || got[0].Platform != "r-claude-only:claudecode" {
		t.Fatalf("enabled targets = %v", got)
	}
	if got := resolveTargets(m, "/local", "https://host/repo", nil); len(got) != 1 || got[0].Platform != "r-claude-only:claudecode" {
		t.Fatalf("project targets = %v", got)
	}
	if got := m.RemoteProjects(); len(got) != 1 || got[0].Platform != "r-claude-only:claudecode" {
		t.Fatalf("remote projects = %v", got)
	}
}

// Run the normal supervisor up to publication, then cancel its wait loop.
func (m *Manager) connectAndRegisterTest(t *testing.T, mr *managedRemote) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	m.beforeAdapterRegister = cancel
	m.connectAndRegister(ctx, mr, mr.localID)
}
