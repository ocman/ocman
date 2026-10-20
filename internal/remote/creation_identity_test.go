package remote

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type identityOwnerPlatform struct {
	*fakePlatform
	catalogue *sessioncatalog.Store
	creates   atomic.Int32
	rules     atomic.Int32
	disposes  atomic.Int32
	forks     atomic.Int32
}

func (p *identityOwnerPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: "opencode", SourceKey: "owner-source"}, nil
}

func (p *identityOwnerPlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	rows, err := p.catalogue.List(ctx, "owner-instance", 10, 0)
	if err != nil {
		return nil, err
	}
	if len(rows) != 1 || rows[0].Origin != sessioncatalog.Routine || rows[0].OriginID != "routine" || rows[0].CreationState != sessioncatalog.Creating {
		return nil, fmt.Errorf("owner origin not reserved before execution: %+v", rows)
	}
	p.creates.Add(1)
	return &platforms.CreateSessionResponse{ID: "native"}, nil
}

func (p *identityOwnerPlatform) SetPermissionRules(context.Context, platforms.SetPermissionRulesRequest) error {
	p.rules.Add(1)
	return nil
}
func (p *identityOwnerPlatform) DisposeSession(context.Context, platforms.DisposeSessionRequest) error {
	p.disposes.Add(1)
	return nil
}

func (p *identityOwnerPlatform) ForkSession(context.Context, platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	p.forks.Add(1)
	return &platforms.CreateSessionResponse{ID: "native-fork"}, nil
}

func TestRemoteCreationReservesAndBindsOnOwnerOnly(t *testing.T) {
	db, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	p := &identityOwnerPlatform{fakePlatform: &fakePlatform{id: "opencode"}, catalogue: catalogue}
	registry := platforms.NewRegistry()
	registry.Register(p)
	ownerSessions := sessionsvc.New(registry, sessionsvc.Hooks{})
	ownerSessions.WithSessionCatalogue(catalogue, "owner-instance")
	server := NewServer(registry, localStubHost{}, "owner-instance", "test").UseSessions(ownerSessions)
	listener, err := NewListener(ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, server)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	defer listener.Stop()
	conn := NewRemoteConn("grpc://"+listener.Addr(), "token")
	if err := conn.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	remote := newRemotePlatform(conn, "opencode", func() string { return "owner" })
	hubRegistry := platforms.NewRegistry()
	hubRegistry.Register(remote)
	hubSessions := sessionsvc.New(hubRegistry, sessionsvc.Hooks{})
	// A broken hub catalogue must not affect owner-local reservation.
	hubSessions.WithSessionCatalogue(nil, "hub-instance")
	req := platforms.CreateSessionRequest{Directory: "/repo", Creation: &platforms.CreationIdentity{RequestKey: "run", Origin: "routine", OriginID: "routine", Configured: true, PermissionRules: []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}}}}
	for range 2 {
		resp, err := hubSessions.Create(t.Context(), string(remote.ID()), req)
		if err != nil || resp.ID != "native" {
			t.Fatalf("remote create: %+v %v", resp, err)
		}
	}
	if p.creates.Load() != 1 {
		t.Fatalf("replayed native execution %d times", p.creates.Load())
	}
	rows, err := catalogue.List(t.Context(), "owner-instance", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].NativeID != "native" || rows[0].CreationState != sessioncatalog.Ready || rows[0].SourceKey != "owner-source" {
		t.Fatalf("owner binding: %+v %v", rows, err)
	}
	if p.rules.Load() != 1 {
		t.Fatalf("initial configuration applied %d times", p.rules.Load())
	}
	hubDB, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = hubDB.Close() }()
	hubCatalogue, err := sessioncatalog.New(hubDB)
	if err != nil {
		t.Fatal(err)
	}
	hubSessions.WithSessionCatalogue(hubCatalogue, "hub-instance")
	identity, err := hubSessions.LookupSessionIdentity(t.Context(), string(remote.ID()), platforms.SessionIdentityRequest{NativeID: "native"})
	if err != nil || identity.ID != rows[0].ID || identity.OwnerID != "owner-instance" || identity.Platform != string(remote.ID()) {
		t.Fatalf("hub reminted owner identity: %+v %v", identity, err)
	}
	batch, err := hubSessions.LookupSessionIdentities(t.Context(), string(remote.ID()), []string{"native"})
	if err != nil || len(batch) != 1 || batch[0].ID != identity.ID {
		t.Fatalf("remote identity batch: %+v %v", batch, err)
	}
	if mirrored, err := hubCatalogue.Get(t.Context(), identity.ID); err != nil || mirrored.OwnerID != "owner-instance" || mirrored.NativeID != "native" {
		t.Fatalf("owner identity not persisted on hub: %+v %v", mirrored, err)
	}
	if resolved, err := hubSessions.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: identity.ID}); err != nil || resolved.ID != identity.ID {
		t.Fatalf("owned ID required native rediscovery: %+v %v", resolved, err)
	}
	for range 2 {
		resp, err := hubSessions.Fork(t.Context(), string(remote.ID()), platforms.ForkSessionRequest{SessionID: "native", MessageID: "message", Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user"}})
		if err != nil || resp.ID != "native-fork" {
			t.Fatalf("remote fork: %+v %v", resp, err)
		}
	}
	fork, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner-instance", Platform: "opencode", SourceKey: "owner-source", NativeID: "native-fork"})
	if err != nil || fork.ParentID != rows[0].ID || p.forks.Load() != 1 {
		t.Fatalf("remote fork identity not owner-scoped: %+v %v calls=%d", fork, err, p.forks.Load())
	}
	if err := remote.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: "native"}); err != nil {
		t.Fatal(err)
	}
	if p.disposes.Load() != 1 {
		t.Fatal("remote disposal did not reach owner")
	}
	if err := remote.DisposeSession(t.Context(), platforms.DisposeSessionRequest{}); err == nil {
		t.Fatal("empty disposal accepted")
	}
	if p.disposes.Load() != 1 {
		t.Fatal("invalid disposal reached platform")
	}
	conn.Close()
	if _, err := hubSessions.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: identity.ID}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("disconnected owner did not fail closed: %v", err)
	}
	if err := remote.DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: "native"}); !errors.Is(err, platforms.ErrRemoteOffline) {
		t.Fatalf("offline disposal: %v", err)
	}
}
