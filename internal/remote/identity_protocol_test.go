package remote

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
)

type identityProtocolPeer struct {
	pb.UnimplementedOcmanServer
	version atomic.Int32
}

func (s *identityProtocolPeer) Hello(context.Context, *pb.HelloReq) (*pb.HelloResp, error) {
	return &pb.HelloResp{ProtocolVersion: s.version.Load(), InstanceId: "owner", Platforms: []string{"opencode"}}, nil
}

func startIdentityProtocolPeer(t *testing.T, version int32) (*identityProtocolPeer, *RemoteConn) {
	t.Helper()
	peer := &identityProtocolPeer{}
	peer.version.Store(version)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	pb.RegisterOcmanServer(server, peer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn := NewRemoteConn("grpc://"+listener.Addr().String(), "")
	t.Cleanup(conn.Close)
	return peer, conn
}

func TestRemoteV7RejectedBeforeIdentityDependentOperations(t *testing.T) {
	_, conn := startIdentityProtocolPeer(t, 7)
	if err := conn.Connect(t.Context()); err == nil || !strings.Contains(err.Error(), "upgrade ocman on both machines") {
		t.Fatalf("legacy owner accepted: %v", err)
	}
	if conn.Client() != nil || conn.Health() != HealthIncompatible {
		t.Fatalf("legacy owner retained execution authority: client %v health %s", conn.Client() != nil, conn.Health())
	}
}

func TestRemoteProtocolDowngradeDropsIdentityAuthority(t *testing.T) {
	peer, conn := startIdentityProtocolPeer(t, ProtocolVersion)
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	previous := healthPingInterval
	healthPingInterval = 5 * time.Millisecond
	t.Cleanup(func() { healthPingInterval = previous })
	peer.version.Store(7)
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	go func() { conn.WaitForDrop(ctx); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("downgraded owner kept its adapters alive")
	}
	if conn.Health() != HealthIncompatible {
		t.Fatalf("protocol downgrade not detected: %s", conn.Health())
	}
}
