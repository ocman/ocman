package remote

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func TestRemoteConnDetectsChangedPlatformsWithSameIdentity(t *testing.T) {
	previous := healthPingInterval
	healthPingInterval = time.Millisecond
	t.Cleanup(func() { healthPingInterval = previous })
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	conn := NewRemoteConn(startRealRemote(t, "tok", "stable-id", reg), "tok")
	t.Cleanup(conn.Close)
	for _, add := range []bool{true, false} {
		if err := conn.Connect(t.Context()); err != nil {
			t.Fatal(err)
		}
		if add {
			reg.Register(&fakePlatform{id: "claudecode"})
		} else {
			reg.Unregister("claudecode")
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		conn.WaitForDrop(ctx)
		if ctx.Err() != nil {
			t.Fatal("platform change was not detected")
		}
		cancel()
	}
}

type blockedInventoryServer struct {
	*Server
	started chan struct{}
	resume  chan struct{}
}

func (s blockedInventoryServer) Call(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if req.Method != "Projects" {
		return s.Server.Call(ctx, req)
	}
	close(s.started)
	<-s.resume
	return jsonResp([]ProjectIdentity{{Dir: "/remote/repo"}}, nil)
}

func TestInventoryRefreshCannotRestoreDisconnectedRemote(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := blockedInventoryServer{
		Server:  NewServer(platforms.NewRegistry(), localStubHost{}, "inventory-id", "test"),
		started: make(chan struct{}), resume: make(chan struct{}),
	}
	server := grpc.NewServer()
	pb.RegisterOcmanServer(server, srv)
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(server.Stop)
	conn := NewRemoteConn("grpc://"+ln.Addr().String(), "")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "opencode")
	mr := &managedRemote{localID: 1, conn: conn}
	m.remotes[1] = mr
	if !m.publishAdapters(1, mr, []*remotePlatform{newRemotePlatform(conn, "opencode", nil)}, newRemoteHost(conn)) {
		t.Fatal("publication failed")
	}
	done := make(chan struct{})
	go func() {
		m.refreshInventory(t.Context(), mr)
		close(done)
	}()
	<-srv.started
	m.unregisterAdapters(mr)
	close(srv.resume)
	<-done
	if len(m.RemoteProjects()) != 0 {
		t.Fatal("in-flight refresh restored disconnected inventory")
	}
}
