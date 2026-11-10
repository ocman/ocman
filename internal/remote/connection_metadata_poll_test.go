package remote

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type mutableConnectionServer struct {
	*Server
	mu          sync.Mutex
	entries     []ConnectionPlatformMetadata
	metadataErr error
}

func (s *mutableConnectionServer) Call(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if req.Method != "ConnectionPlatforms" {
		return s.Server.Call(ctx, req)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return jsonResp(copyConnectionPlatforms(s.entries), s.metadataErr)
}
func startMutableConnectionOwner(t *testing.T) (*mutableConnectionServer, *RemoteConn) {
	t.Helper()
	previous := healthPingInterval
	healthPingInterval = 2 * time.Millisecond
	t.Cleanup(func() { healthPingInterval = previous })
	reg := platforms.NewRegistry()
	for _, key := range []string{"cnx_personal", "cnx_work"} {
		if err := reg.RegisterConnection(platforms.ID(key), &fakePlatform{id: "codex"}); err != nil {
			t.Fatal(err)
		}
	}
	s := &mutableConnectionServer{Server: NewServer(reg, localStubHost{}, "owner", "test"), entries: []ConnectionPlatformMetadata{
		{ID: "cnx_personal", Kind: "codex", SourceKey: "personal-source", Default: true, Selectable: true},
		{ID: "cnx_work", Kind: "codex", SourceKey: "work-source", Selectable: true},
	}}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unary, stream := NewAuthInterceptors("token")
	server := grpc.NewServer(grpc.UnaryInterceptor(unary), grpc.StreamInterceptor(stream))
	pb.RegisterOcmanServer(server, s)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	c := NewRemoteConn("grpc://"+listener.Addr().String(), "token")
	if err := c.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return s, c
}
func pollConnection(t *testing.T, c *RemoteConn) (context.Context, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { c.WaitForDrop(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return ctx, done
}
func waitForConnectionMetadata(t *testing.T, c *RemoteConn, want string) {
	t.Helper()
	timeout := time.NewTimer(time.Second)
	defer timeout.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if c.creationPlatform("codex") == want {
			return
		}
		select {
		case <-timeout.C:
			t.Fatalf("mutable default not refreshed: got %q want %q", c.creationPlatform("codex"), want)
		case <-tick.C:
		}
	}
}

func TestRemoteMutableDefaultRefreshesWithoutReconnectOrChangingExplicitAccount(t *testing.T) {
	s, c := startMutableConnectionOwner(t)
	_, done := pollConnection(t, c)
	client := c.Client()
	m := NewManager(platforms.NewRegistry(), hostsvc.NewRouter(localStubHost{}), nil, "codex")
	mr := &managedRemote{localID: 1, conn: c}
	m.remotes[1] = mr
	if !m.publishAdapters(1, mr, []*remotePlatform{newRemotePlatform(c, "cnx_personal", nil), newRemotePlatform(c, "cnx_work", nil)}, newRemoteHost(c)) {
		t.Fatal("publish failed")
	}
	s.mu.Lock()
	s.entries[0].Default = false
	s.entries[1].Default = true
	s.mu.Unlock()
	waitForConnectionMetadata(t, c, "cnx_work")
	if targets := m.EnabledRemotes(); len(targets) != 1 || targets[0].Platform != "r-owner:cnx_work" {
		t.Fatal(targets)
	}
	if c.creationPlatform("cnx_personal") != "cnx_personal" {
		t.Fatal("explicit draft changed accounts with the default")
	}
	if c.Client() != client {
		t.Fatal("default change replaced transport")
	}
	select {
	case <-done:
		t.Fatal("default change forced reconnect")
	default:
	}
	s.mu.Lock()
	s.entries[1].Selectable = false
	s.mu.Unlock()
	waitForConnectionMetadata(t, c, "")
	if c.creationPlatform("cnx_work") != "" {
		t.Fatal("unavailable pinned draft switched to sibling")
	}
}

func TestRemoteMetadataPollingRejectsMovedSourcesAndTransportFailures(t *testing.T) {
	for _, mode := range []string{"source-move", "transport"} {
		t.Run(mode, func(t *testing.T) {
			s, c := startMutableConnectionOwner(t)
			_, done := pollConnection(t, c)
			s.mu.Lock()
			if mode == "source-move" {
				s.entries[1].SourceKey = "personal-source"
			} else {
				s.metadataErr = status.Error(codes.Unavailable, "owner unavailable")
			}
			s.mu.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("failed metadata refresh left connection healthy")
			}
			if c.Health() != HealthOffline {
				t.Fatal("invalid metadata remained available", c.Health())
			}
			snapshot := c.ConnectionPlatforms()
			if snapshot[1].SourceKey != "work-source" {
				t.Fatal("invalid source proof published", snapshot)
			}
		})
	}
}

func TestMetadataRefreshCannotPublishIntoSupersedingTransport(t *testing.T) {
	_, c := startMutableConnectionOwner(t)
	c.mu.RLock()
	original := c.conn
	c.mu.RUnlock()
	next := c.ConnectionPlatforms()
	next[0].Default = false
	next[1].Default = true
	payload, err := marshalJSON(next)
	if err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.conn = new(grpc.ClientConn)
	c.mu.Unlock()
	t.Cleanup(func() { c.mu.Lock(); c.conn = original; c.mu.Unlock() })
	err = c.refreshConnectionMetadata(t.Context(), original, boundaryClient{payload: payload}, "owner", []string{"cnx_personal", "cnx_work"})
	if !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatal(err)
	}
	c.markMetadataOffline(original)
	if c.Health() != HealthConnected || c.creationPlatform("codex") != "cnx_personal" {
		t.Fatal("old metadata response overwrote superseding connection")
	}
}
