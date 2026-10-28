package remote

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/sessionsvc"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestCanonicalRemoteIdentityCarriesNoNativeBindingAndOwnerAllocatesAllIDs(t *testing.T) {
	ownerDB := statetest.Open(t, sessioncatalog.Schema)
	ownerCatalogue, err := sessioncatalog.New(ownerDB)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner-instance", Platform: "opencode", SourceKey: "owner-source"}
	if _, err := ownerCatalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	native := &identityOwnerPlatform{fakePlatform: &fakePlatform{id: "opencode"}, catalogue: ownerCatalogue}
	adapter, err := owned.New(t.Context(), native, ownerCatalogue, statev2test.Open(t), "owner-instance")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	ownerSessions := sessionsvc.New(registry, sessionsvc.Hooks{})
	ownerSessions.WithSessionCatalogue(ownerCatalogue, "owner-instance")
	server := NewServer(registry, localStubHost{}, "owner-instance", "test").UseSessions(ownerSessions)
	listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, server)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	conn := NewRemoteConn("grpc://"+listener.Addr(), "token")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	// Exercise the v9 payload contract before the application-wide protocol
	// cutover. Negotiation remains v8 until every production path is wired.
	conn.mu.Lock()
	conn.protocolVersion = canonicalSessionProtocol
	conn.mu.Unlock()
	remote := newRemotePlatform(conn, "opencode", func() string { return "owner" })
	rows, err := remote.ResolveLegacyReferences(t.Context(), []string{"native"})
	if err != nil || len(rows) != 1 || !sessioncatalog.IsID(rows["native"].ID) || rows["native"].NativeID != "" {
		t.Fatalf("canonical owner response: %+v %v", rows, err)
	}
	id := rows["native"].ID
	hubDB := statetest.Open(t, sessioncatalog.Schema)
	hubCatalogue, err := sessioncatalog.New(hubDB)
	if err != nil {
		t.Fatal(err)
	}
	hubRegistry := platforms.NewRegistry()
	hubRegistry.Register(remote)
	hubSessions := sessionsvc.New(hubRegistry, sessionsvc.Hooks{})
	hubSessions.WithSessionCatalogue(hubCatalogue, "hub-instance")
	resolved, err := hubSessions.ResolveLegacyReferences(t.Context(), string(remote.ID()), []string{"native"})
	if err != nil || resolved["native"].ID != id || resolved["native"].NativeID != "" {
		t.Fatalf("hub changed owner allocation: %+v %v", resolved, err)
	}
	metadata, err := hubCatalogue.GetCanonical(t.Context(), id)
	if err != nil || metadata.OwnerID != "owner-instance" || metadata.NativeID != "" {
		t.Fatalf("hub persisted native execution authority: %+v %v", metadata, err)
	}
	if _, err := hubCatalogue.Get(t.Context(), id); !errors.Is(err, sessioncatalog.ErrNotFound) {
		t.Fatalf("hub installed native binding: %v", err)
	}
	info, err := hubSessions.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: id})
	if err != nil || info.NativeID != "" || hubSessions.CoreSessionReference(info) != id {
		t.Fatalf("core identity changed across RPC: %+v %v", info, err)
	}
	if native.creates.Load() != 0 {
		t.Fatal("identity migration allocated another native session")
	}
}
