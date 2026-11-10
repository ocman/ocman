package sessionsvc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
	"github.com/ocman/ocman/internal/testutil"
)

type connectionRig struct {
	svc         *Service
	store       *platformconnections.Store
	router      *platformconnections.Router
	catalogue   *sessioncatalog.Store
	connections []platformconnections.Connection
	native      []*identityPlatform
}

type inspectedIdentityPlatform struct{ *identityPlatform }

func (*inspectedIdentityPlatform) InspectConnection(context.Context) (platforms.ConnectionInspection, error) {
	return platforms.ConnectionInspection{Ready: true, IsolationSupported: true}, nil
}

func newConnectionRig(t *testing.T) connectionRig {
	t.Helper()
	db := statetest.Open(t, sessioncatalog.Schema, platformconnections.Schema)
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	store := platformconnections.New(db)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	r := connectionRig{store: store, router: platformconnections.NewRouter(store), catalogue: catalogue}
	registry := platforms.NewRegistry()
	for _, name := range []string{"Personal", "Work"} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "opencode", Name: name, Configuration: "isolated", Directory: "/accounts/" + name, Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		native := &identityPlatform{fakePlatform: &fakePlatform{id: "opencode", available: true}, sourceKey: c.Directory}
		a, err := owned.New(t.Context(), &inspectedIdentityPlatform{native}, catalogue, statev2test.Open(t), "owner")
		if err != nil {
			t.Fatal(err)
		}
		if len(r.connections) == 0 {
			registry.Register(a) // Legacy registry still has only one kind.
		}
		if err := r.router.Bind(t.Context(), c.ID, a); err != nil {
			t.Fatal(err)
		}
		r.connections, r.native = append(r.connections, c), append(r.native, native)
	}
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	r.svc = New(registry, Hooks{})
	if err := r.svc.WithConnections(r.router, catalogue, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetDefault(t.Context(), r.connections[0].ID); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNamedCreationPinsConnectionBeforeNativeCallAndRetry(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	r.native[1].before = func(ctx context.Context, req platforms.CreateSessionRequest) {
		id, err := r.store.SessionConnection(ctx, req.Creation.SessionID)
		if err != nil || id != selected.ID {
			t.Fatalf("native call preceded account binding: %q %v", id, err)
		}
	}
	response, err := r.svc.Create(t.Context(), selected.ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetDefault(t.Context(), r.connections[0].ID); err != nil {
		t.Fatal(err)
	}
	retry, err := r.svc.Create(t.Context(), "", identityRequest())
	if err != nil || retry.ID != response.ID || len(r.native[0].creates) != 0 || len(r.native[1].creates) != 1 {
		t.Fatalf("retry changed account or repeated native side effect: %+v %v", retry, err)
	}
	if _, err := r.svc.Create(t.Context(), r.connections[0].ID, identityRequest()); !errors.Is(err, platformconnections.ErrSessionConflict) {
		t.Fatalf("explicit retry switched accounts: %v", err)
	}
	for _, scope := range []string{"", selected.ID, selected.Kind} {
		if err := r.svc.Abort(t.Context(), scope, platforms.AbortRequest{SessionID: response.ID}); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.native[0].aborts) != 0 || len(r.native[1].aborts) != 3 {
		t.Fatal("subsequent operations used the kind-keyed registry adapter")
	}
	if _, err := r.svc.resolve(t.Context(), response.ID, r.connections[0].ID); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("explicit scope overrode recorded session account: %v", err)
	}
}

func TestNamedCreationAndExistingSessionFailClosed(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	response, err := r.svc.Create(t.Context(), selected.ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.store.SetEnabled(t.Context(), selected.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.Create(t.Context(), "", identityRequest()); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatalf("retry fell back after disable: %v", err)
	}
	if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: response.ID}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatalf("existing session fell back after disable: %v", err)
	}
	if len(r.native[0].creates) != 0 || len(r.native[0].aborts) != 0 {
		t.Fatal("disabled account routed to sibling")
	}
}

func TestNamedScopeResolvesToConnectionNotKind(t *testing.T) {
	r := newConnectionRig(t)
	c := r.connections[1]
	if err := r.store.SetDefault(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	got, err := r.svc.ResolvePlatformID(t.Context(), "")
	if err != nil || got != c.ID || !r.svc.KnownPlatform(c.ID) || r.svc.KnownPlatform("missing") {
		t.Fatalf("selection lost named identity: %q %v", got, err)
	}
	if _, err := r.svc.ResolvePlatformID(t.Context(), "opencode"); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatalf("unmapped legacy kind selected arbitrary account: %v", err)
	}
	if _, err := r.svc.ResolvePlatformID(t.Context(), platforms.CompoundPlatformID("remote", "opencode")); err == nil {
		t.Fatal("unknown explicit remote fell back to local default")
	}
}

func TestConnectionRoutingRejectsInvalidComposition(t *testing.T) {
	r := newConnectionRig(t)
	for _, tc := range []struct {
		router    *platformconnections.Router
		catalogue *sessioncatalog.Store
		owner     string
	}{
		{nil, r.catalogue, "owner"}, {r.router, nil, "owner"}, {r.router, r.catalogue, ""},
	} {
		if err := r.svc.WithConnections(tc.router, tc.catalogue, tc.owner); !errors.Is(err, platformconnections.ErrInvalid) {
			t.Fatal("invalid connection composition accepted")
		}
	}
	if err := New(&fakeRegistry{}, Hooks{}).WithConnections(r.router, r.catalogue, "owner"); !errors.Is(err, platformconnections.ErrInvalid) {
		t.Fatal("native-only registry accepted named canonical routing")
	}
}

func TestNamedForkInheritsParentsAccountRatherThanDefault(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	parent, err := r.svc.Create(t.Context(), selected.ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	request := platforms.ForkSessionRequest{SessionID: parent.ID, Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user"}}
	child, err := r.svc.Fork(t.Context(), "", request)
	if err != nil {
		t.Fatal(err)
	}
	connectionID, err := r.store.SessionConnection(t.Context(), child.ID)
	if err != nil || connectionID != selected.ID || len(r.native[0].forks) != 0 || len(r.native[1].forks) != 1 {
		t.Fatalf("fork lost parent account: %q %v", connectionID, err)
	}
	if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: child.ID}); err != nil {
		t.Fatal(err)
	}
	retry, err := r.svc.Fork(t.Context(), "", request)
	if err != nil || retry.ID != child.ID || len(r.native[1].forks) != 1 {
		t.Fatalf("fork retry changed identity: %+v %v", retry, err)
	}
}

func TestNamedCreationUncertainOutcomeDoesNotSwitchOrRepeat(t *testing.T) {
	r := newConnectionRig(t)
	r.native[1].createErr = errors.New("lost connection after submission")
	_, err := r.svc.Create(t.Context(), r.connections[1].ID, identityRequest())
	id := pendingID(t, err)
	if connectionID, err := r.store.SessionConnection(t.Context(), id); err != nil || connectionID != r.connections[1].ID {
		t.Fatalf("uncertain creation lost account: %q %v", connectionID, err)
	}
	r.native[1].createErr = nil
	if _, err := r.svc.Create(t.Context(), "", identityRequest()); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatalf("uncertain retry started a new session: %v", err)
	}
	if len(r.native[0].creates) != 0 || len(r.native[1].creates) != 0 {
		t.Fatal("uncertain retry repeated native creation")
	}
	if _, err := r.svc.resolve(t.Context(), id, ""); !errors.Is(err, sessioncatalog.ErrCreating) {
		t.Fatal("creating identity admitted to execution")
	}
}

func TestNamedCreationExplicitIdentityRetryAndUnboundHistory(t *testing.T) {
	r := newConnectionRig(t)
	request := identityRequest()
	response, err := r.svc.Create(t.Context(), r.connections[1].ID, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Creation.SessionID = response.ID
	retry, err := r.svc.Create(t.Context(), "", request)
	if err != nil || retry.ID != response.ID {
		t.Fatalf("explicit identity retry failed: %+v %v", retry, err)
	}
	// Another canonical identity exists in history but has no account routing
	// evidence. Supplying it must not claim it for the current default.
	legacy, err := r.catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "external"}, []sessioncatalog.NativeSession{{ID: "external-native", Directory: "/repo"}})
	if err != nil || legacy.Created != 1 {
		t.Fatalf("external identity setup: %+v %v", legacy, err)
	}
	row, err := r.catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "external", NativeID: "external-native"})
	if err != nil {
		t.Fatal(err)
	}
	request.Creation = &platforms.CreationIdentity{SessionID: row.ID, RequestKey: "claim-history", Origin: "user"}
	if _, err := r.svc.Create(t.Context(), "", request); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatalf("unbound history acquired default-account authority: %v", err)
	}
	if _, err := r.svc.resolve(t.Context(), row.ID, ""); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatalf("unbound history used a default adapter: %v", err)
	}
}

func TestNamedCatalogSelectionReturnsPinnedConnectionID(t *testing.T) {
	r := newConnectionRig(t)
	p, id, err := r.svc.catalogAdapter(t.Context(), "")
	if err != nil || id != r.connections[0].ID || p.ID() != "opencode" {
		t.Fatalf("catalogue selection conflated kind and instance: %q %v", id, err)
	}
	if _, _, err := r.svc.catalogAdapter(t.Context(), "missing"); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal(err)
	}
}

func TestImportedSessionRoutesOnlyThroughProvenPhysicalSource(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	if err := r.store.BindSource(t.Context(), selected.ID, selected.Kind, r.native[1].sourceKey); err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: r.native[1].sourceKey}
	if _, err := r.catalogue.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: "imported-native", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	row, err := r.catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: source.OwnerID, Platform: source.Platform, SourceKey: source.SourceKey, NativeID: "imported-native"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: row.ID}); err != nil {
		t.Fatal(err)
	}
	id, err := r.store.SessionConnection(t.Context(), row.ID)
	if err != nil || id != selected.ID || len(r.native[0].aborts) != 0 || len(r.native[1].aborts) != 1 {
		t.Fatalf("import selected default rather than physical source: %q %v", id, err)
	}
}

func TestPreparedSendCannotBypassLaterConnectionFence(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	created, err := r.svc.Create(t.Context(), selected.ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	deliver, err := r.svc.PrepareSendMessage(t.Context(), "", platforms.SendMessageRequest{SessionID: created.ID, Message: "held prompt", PreserveSelections: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.router.Detach(t.Context(), selected.ID, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := deliver(t.Context()); !errors.Is(err, platformconnections.ErrUnavailable) || !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatalf("prepared delivery bypassed removal fence: %v", err)
	}
	if len(r.native[1].sent) != 0 {
		t.Fatal("fenced prepared delivery reached native account")
	}
	if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: created.ID}); err != nil {
		t.Fatal("abort must remain available while draining", err)
	}
}

func TestRemovalWaitsForAdmittedCreationAndAllowsAbort(t *testing.T) {
	r := newConnectionRig(t)
	selected := r.connections[1]
	parent, err := r.svc.Create(t.Context(), selected.ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	r.native[1].createResp = &platforms.CreateSessionResponse{ID: "second-native"}
	r.native[1].before = func(context.Context, platforms.CreateSessionRequest) { close(started); <-release }
	created := make(chan error, 1)
	request := identityRequest()
	request.Creation.RequestKey = "second-request"
	go func() { _, err := r.svc.Create(t.Context(), selected.ID, request); created <- err }()
	<-started
	removed := make(chan struct{})
	detached := make(chan error, 1)
	go func() {
		detached <- r.router.Detach(t.Context(), selected.ID, func() error { close(removed); return r.store.Remove(t.Context(), selected.ID) })
	}()
	testutil.WaitFor(t, 5*time.Second, "account admission fenced", func() bool {
		done, err := r.router.Begin(t.Context(), selected.ID)
		if err == nil {
			done()
			return false
		}
		return errors.Is(err, platformconnections.ErrUnavailable)
	})
	select {
	case <-removed:
		t.Fatal("removed an account before admitted creation finished")
	default:
	}
	if err := r.svc.Abort(t.Context(), "", platforms.AbortRequest{SessionID: parent.ID}); err != nil {
		t.Fatal("abort was blocked by removal fence", err)
	}
	close(release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-detached; err != nil {
		t.Fatal(err)
	}
}
