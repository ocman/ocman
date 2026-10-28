package remote

import (
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func TestCanonicalHelloAdvertisesV9AndStrictConnectionsRejectLegacyOwners(t *testing.T) {
	hello, err := canonicalRPCServer(t).Hello(t.Context(), &pb.HelloReq{})
	if err != nil || hello.ProtocolVersion != ProtocolVersion {
		t.Fatalf("canonical negotiation: %+v %v", hello, err)
	}
	conn := connectedPair(t, &fakePlatform{id: "opencode"}).requiringCanonical(true)
	if err := conn.Connect(t.Context()); err == nil || conn.Health() != HealthIncompatible {
		t.Fatalf("legacy owner admitted to canonical connection: %v %s", err, conn.Health())
	}
	if conn.acceptsSessionProtocol(legacySessionProtocol) || !conn.acceptsSessionProtocol(ProtocolVersion) {
		t.Fatal("strict session contract policy is wrong")
	}
}

func TestCanonicalManagerDoesNotPublishLegacyHostOrPlatform(t *testing.T) {
	registry := platforms.NewRegistry()
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	router := hostsvc.NewRouter(localStubHost{})
	mgr := NewManager(registry, router, nil, "opencode")
	t.Cleanup(mgr.Stop)
	conn := &RemoteConn{remoteID: "owner", protocolVersion: int(legacySessionProtocol), health: HealthConnected}
	mr := &managedRemote{localID: 1, conn: conn}
	mgr.remotes[1] = mr
	if mgr.publishAdapters(1, mr, []*remotePlatform{newRemotePlatform(conn, "opencode", nil)}, newRemoteHost(conn)) {
		t.Fatal("legacy owner gained canonical routing authority")
	}
	if len(router.Remotes()) != 0 || len(registry.Platforms()) != 0 || conn.Health() != HealthIncompatible {
		t.Fatal("legacy publication was only partially rejected")
	}
}
