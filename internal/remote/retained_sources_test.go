package remote

import (
	"context"
	"testing"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
	"google.golang.org/grpc"
)

func TestAuthenticatedRetainedRemoteSourceSurvivesHubRestartButCannotMutate(t *testing.T) {
	db := statetest.Open(t, sessioncatalog.Schema, platformconnections.Schema)
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	oldSource := sessioncatalog.Source{OwnerID: "owner-instance", Platform: "opencode", SourceKey: "old-owner-source"}
	if _, err := catalogue.Reconcile(t.Context(), oldSource, []sessioncatalog.NativeSession{{ID: "retained-native", Directory: "/old/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: oldSource.OwnerID, Platform: oldSource.Platform, SourceKey: oldSource.SourceKey, NativeID: "retained-native"})
	if err != nil {
		t.Fatal(err)
	}
	store := platformconnections.New(db)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: "Work", Configuration: "existing", Directory: "/config/work", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"owner-source", oldSource.SourceKey} {
		if err := store.BindSource(t.Context(), c.ID, c.Kind, source); err != nil {
			t.Fatal(err)
		}
	}
	native := &identityOwnerPlatform{fakePlatform: &fakePlatform{id: "opencode"}, catalogue: catalogue}
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), oldSource.OwnerID)
	if err != nil {
		t.Fatal(err)
	}
	adapter.WithReadSources([]string{oldSource.SourceKey})
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
	if err := ownerSessions.WithConnections(router, catalogue, oldSource.OwnerID); err != nil {
		t.Fatal(err)
	}
	transport := startTestServer(t, "token", NewServer(registry, localStubHost{}, oldSource.OwnerID, "test").UseSessions(ownerSessions))
	client := pb.NewOcmanClient(transport)
	metadata, err := loadConnectionPlatforms(t.Context(), client, []string{c.ID})
	if err != nil {
		t.Fatal(err)
	}
	conn := &RemoteConn{client: client, remoteID: oldSource.OwnerID, protocolVersion: canonicalSessionProtocol, health: HealthConnected, connectionPlatforms: metadata}
	remote := newRemotePlatform(conn, c.ID, func() string { return "owner" })
	owner, kind, sources := remote.CanonicalSources()
	if owner != oldSource.OwnerID || kind != "opencode" || len(sources) != 2 {
		t.Fatalf("retained source proof: %q %q %v", owner, kind, sources)
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
	if _, err := hub.LookupSessionIdentity(t.Context(), string(remote.ID()), platforms.SessionIdentityRequest{ID: row.ID}); err != nil {
		t.Fatal(err)
	}
	// Reconstruct the hub with no session reverse cache. The owner-authenticated
	// retained source set is the only route to this old physical source.
	restartedRegistry := platforms.NewRegistry()
	restartedRegistry.Register(newRemotePlatform(conn, c.ID, nil))
	restarted := sessionsvc.New(restartedRegistry, sessionsvc.Hooks{})
	restarted.WithSessionCatalogue(hubCatalogue, "hub-instance")
	info, err := restarted.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: row.ID})
	if err != nil || info == nil || info.ID != row.ID || info.SourceKey != oldSource.SourceKey {
		t.Fatalf("retained restart lookup: %+v %v", info, err)
	}
	stored, err := hubCatalogue.GetCanonical(t.Context(), row.ID)
	if err != nil || stored.SourceKey != oldSource.SourceKey || stored.Platform != "opencode" || stored.NativeID != "" {
		t.Fatalf("retained mirror changed provenance: %+v %v", stored, err)
	}
	if err := restarted.SetPermissionRules(t.Context(), "", platforms.SetPermissionRulesRequest{SessionID: row.ID}); err == nil {
		t.Fatal("retained source acquired execution authority")
	}
	if native.rules.Load() != 0 {
		t.Fatal("retained mutation reached the active native account")
	}
}

type metadataClient struct {
	pb.OcmanClient
	entries []ConnectionPlatformMetadata
}

func (c metadataClient) Call(context.Context, *pb.CallReq, ...grpc.CallOption) (*pb.JsonResp, error) {
	return jsonResp(c.entries, nil)
}

func TestRetainedConnectionMetadataValidationAndCopies(t *testing.T) {
	valid := ConnectionPlatformMetadata{ID: "cnx_work", Kind: "opencode", SourceKey: "active", SourceKeys: []string{"active", "retained"}}
	for _, keys := range [][]string{{"active", ""}, {"active", "active"}, {"retained"}} {
		entry := valid
		entry.SourceKeys = keys
		if _, err := loadConnectionPlatforms(t.Context(), metadataClient{entries: []ConnectionPlatformMetadata{entry}}, []string{entry.ID}); err == nil {
			t.Fatalf("invalid source proof accepted: %v", keys)
		}
	}
	conn := &RemoteConn{remoteID: "owner", connectionPlatforms: []ConnectionPlatformMetadata{valid}}
	snapshot := conn.ConnectionPlatforms()
	snapshot[0].SourceKeys[0] = "changed"
	remote := newRemotePlatform(conn, valid.ID, nil)
	_, _, sources := remote.CanonicalSources()
	sources[0] = "changed"
	_, _, again := remote.CanonicalSources()
	if again[0] != "active" {
		t.Fatal("source allowlist is mutable")
	}
	legacy := newRemotePlatform(&RemoteConn{remoteID: "owner", connectionPlatforms: []ConnectionPlatformMetadata{{ID: "cnx_legacy", Kind: "opencode", SourceKey: "singular"}}}, "cnx_legacy", nil)
	_, _, single := legacy.CanonicalSources()
	if len(single) != 1 || single[0] != "singular" {
		t.Fatal(single)
	}
	other := valid
	other.ID = "cnx_sibling"
	if err := validateConnectionSourceContinuity([]ConnectionPlatformMetadata{valid}, []ConnectionPlatformMetadata{other}); err == nil {
		t.Fatal("physical source moved to sibling")
	}
	if err := validateConnectionSourceContinuity([]ConnectionPlatformMetadata{valid}, []ConnectionPlatformMetadata{valid}); err != nil {
		t.Fatal(err)
	}
}
