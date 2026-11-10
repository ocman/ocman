package remote

import (
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestNamedRemoteCreationThenUncachedNativeKindIdentityLookup(t *testing.T) {
	db := statetest.Open(t, sessioncatalog.Schema, platformconnections.Schema)
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	store := platformconnections.New(db)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "Work", Configuration: "isolated", Directory: "/config/work", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	native := &identityOwnerPlatform{fakePlatform: &fakePlatform{id: "opencode"}, catalogue: catalogue}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner-instance")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	if err := registry.RegisterConnection(platforms.ID(c.ID), adapter); err != nil {
		t.Fatal(err)
	}
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	router := platformconnections.NewRouter(store)
	if err := router.Bind(t.Context(), c.ID, adapter); err != nil {
		t.Fatal(err)
	}
	ownerSessions := sessionsvc.New(registry, sessionsvc.Hooks{})
	if err := ownerSessions.WithConnections(router, catalogue, "owner-instance"); err != nil {
		t.Fatal(err)
	}
	server := NewServer(registry, localStubHost{}, "owner-instance", "test").UseSessions(ownerSessions)
	transport := startTestServer(t, "token", server)
	conn := &RemoteConn{client: nil, remoteID: "owner-instance", protocolVersion: canonicalSessionProtocol, health: HealthConnected, connectionPlatforms: []ConnectionPlatformMetadata{{ID: c.ID, Kind: "opencode", DisplayName: "Work"}}}
	conn.client = pb.NewOcmanClient(transport)
	metadata, err := loadConnectionPlatforms(t.Context(), conn.client, []string{c.ID})
	if err != nil {
		t.Fatal(err)
	}
	conn.connectionPlatforms = metadata
	remote := newRemotePlatform(conn, c.ID, func() string { return "owner" })
	if owner, kind, source := remote.CanonicalSource(); owner != "owner-instance" || kind != "opencode" || source != "owner-source" {
		t.Fatalf("wrong source proof: %q %q %q", owner, kind, source)
	}
	created, err := remote.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "named-run", Origin: "routine", OriginID: "routine", Configured: true}})
	if err != nil {
		t.Fatal(err)
	}
	// No Sessions() call or reverse lookup cache has run on this hub adapter.
	info, err := remote.SessionIdentity(t.Context(), platforms.SessionIdentityRequest{ID: created.ID})
	if err != nil || info == nil || info.Platform != "opencode" || info.ID != created.ID || info.NativeID != "" {
		t.Fatalf("named identity changed physical kind: %+v %v", info, err)
	}
	rows, err := remote.SessionIdentities(t.Context(), []string{created.ID})
	if err != nil || len(rows) != 1 || rows[0].Platform != "opencode" {
		t.Fatalf("batch identity: %+v %v", rows, err)
	}
	hubDB := statetest.Open(t, sessioncatalog.Schema)
	hubCatalogue, err := sessioncatalog.New(hubDB)
	if err != nil {
		t.Fatal(err)
	}
	hubRegistry := platforms.NewRegistry()
	hubRegistry.Register(remote)
	hub := sessionsvc.New(hubRegistry, sessionsvc.Hooks{})
	hub.WithSessionCatalogue(hubCatalogue, "hub-instance")
	resolved, err := hub.LookupSessionIdentity(t.Context(), string(remote.ID()), platforms.SessionIdentityRequest{ID: created.ID})
	if err != nil || resolved.Platform != string(remote.ID()) {
		t.Fatalf("hub mirror: %+v %v", resolved, err)
	}
	stored, err := hubCatalogue.GetCanonical(t.Context(), created.ID)
	if err != nil || stored.Platform != "opencode" || stored.NativeID != "" {
		t.Fatalf("persisted mirror: %+v %v", stored, err)
	}
	// Rebuild registry and service as on hub restart, keeping only the durable
	// mirror plus authenticated owner metadata, with no reverse-session cache.
	restartedRegistry := platforms.NewRegistry()
	restartedRegistry.Register(newRemotePlatform(conn, c.ID, func() string { return "owner" }))
	restarted := sessionsvc.New(restartedRegistry, sessionsvc.Hooks{})
	restarted.WithSessionCatalogue(hubCatalogue, "hub-instance")
	recovered, err := restarted.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: created.ID})
	if err != nil || recovered.Platform != string(remote.ID()) || recovered.ID != created.ID {
		t.Fatalf("uncached restart mirror: %+v %v", recovered, err)
	}
}
