package remote

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type usageTestPlatform struct {
	*fakePlatform
	usage map[string]platforms.Usage
	seen  chan string
	err   error
}

func (p *usageTestPlatform) SessionUsage(_ context.Context, id string) (map[string]platforms.Usage, error) {
	p.seen <- id
	return p.usage, p.err
}

func TestRemoteSessionUsageConnectedOwner(t *testing.T) {
	want := map[string]platforms.Usage{"same-id": {Cost: 0.5, EstCost: 1, Tokens: platforms.TokenTotals{Input: 10}}, "child": {Cost: 0.25, EstCost: 0.5}}
	owner := &usageTestPlatform{fakePlatform: &fakePlatform{id: "opencode"}, usage: want, seen: make(chan string, 1)}
	registry := platforms.NewRegistry()
	registry.Register(owner)
	listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "secret", TrustedOverlay: true}, NewServer(registry, localStubHost{}, "owner", "test"))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	conn := NewRemoteConn("grpc://"+listener.Addr(), "secret")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	var adapter platforms.Platform = newRemotePlatform(conn, "opencode", func() string { return "Owner" })
	reader, ok := adapter.(platforms.UsageReader)
	if !ok {
		t.Fatal("connected remote does not implement UsageReader")
	}
	got, err := reader.SessionUsage(t.Context(), "same-id")
	if err != nil || !reflect.DeepEqual(got, want) || adapter.ID() != "r-owner:opencode" {
		t.Fatalf("usage=%+v, err=%v, platform=%s", got, err, adapter.ID())
	}
	if id := <-owner.seen; id != "same-id" {
		t.Fatalf("owner received %q", id)
	}
	if _, err := reader.SessionUsage(t.Context(), ""); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty session id: %v", err)
	}
	unauthenticated, err := grpc.NewClient(listener.Addr(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unauthenticated.Close() })
	if _, err := pb.NewUsageClient(unauthenticated).SessionUsage(t.Context(), &pb.SessionRef{Platform: "opencode", SessionId: "same-id"}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated usage call: %v", err)
	}
	registry.Register(&fakePlatform{id: "opencode"})
	if _, err := reader.SessionUsage(t.Context(), "same-id"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("unsupported owner: %v", err)
	}
	if _, err := newRemotePlatform(conn, "missing", func() string { return "Owner" }).SessionUsage(t.Context(), "same-id"); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown owner platform: %v", err)
	}
	conn.Close()
	if got, err := reader.SessionUsage(t.Context(), "same-id"); !errors.Is(err, ErrRemoteOffline) || got != nil {
		t.Fatalf("disconnected owner returned usage: %+v, %v", got, err)
	}
}

func TestRemoteSessionUsageLegacyOwner(t *testing.T) {
	wire := startTestServer(t, "secret", NewServer(platforms.NewRegistry(), localStubHost{}, "owner", "test"))
	conn := &RemoteConn{conn: wire, client: pb.NewOcmanClient(wire), remoteID: "owner", health: HealthConnected}
	adapter := newRemotePlatform(conn, "opencode", func() string { return "Owner" })
	if got, err := adapter.SessionUsage(t.Context(), "same-id"); !errors.Is(err, platforms.ErrUnsupported) || got != nil {
		t.Fatalf("older owner: %+v, %v", got, err)
	}
	if conn.Health() != HealthConnected {
		t.Fatal("older owner was marked offline for a missing optional RPC")
	}
}

func TestUsageServerPropagatesOwnerErrors(t *testing.T) {
	registry := platforms.NewRegistry()
	registry.Register(&usageTestPlatform{fakePlatform: &fakePlatform{id: "opencode"}, seen: make(chan string, 1), err: platforms.ErrPlatformUnreachable})
	server := &usageServer{owner: NewServer(registry, localStubHost{}, "owner", "test")}
	if _, err := server.SessionUsage(t.Context(), &pb.SessionRef{Platform: "opencode", SessionId: "s"}); status.Code(err) != codes.Unavailable {
		t.Fatalf("owner error: %v", err)
	}
}
