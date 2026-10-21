package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type identityPlatform struct {
	*fakePlatform
	sourceKey string
	sourceErr error
	createErr error
	before    func(context.Context, platforms.CreateSessionRequest)
}

func (p *identityPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.ID()), SourceKey: p.sourceKey}, p.sourceErr
}

func (p *identityPlatform) CreateSession(ctx context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if p.before != nil {
		p.before(ctx, req)
	}
	if p.createErr != nil {
		return nil, p.createErr
	}
	return p.fakePlatform.CreateSession(ctx, req)
}

func identityService(t *testing.T) (*Service, *identityPlatform, *sessioncatalog.Store, *state.DB) {
	t.Helper()
	db, err := state.Open(statetest.Path(t, sessioncatalog.Schema), sessioncatalog.Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	p := &identityPlatform{fakePlatform: &fakePlatform{id: "opencode", available: true}, sourceKey: "source"}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{})
	svc.WithSessionCatalogue(catalogue, "owner")
	return svc, p, catalogue, db
}

func identityRequest() platforms.CreateSessionRequest {
	return platforms.CreateSessionRequest{Directory: "/repo", Title: "test", Creation: &platforms.CreationIdentity{RequestKey: "start", Origin: "user"}}
}

func TestCreateReservesRoutineIdentityBeforeNativeSideEffects(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	var ownedID string
	p.before = func(ctx context.Context, req platforms.CreateSessionRequest) {
		rows, err := catalogue.List(ctx, "owner", 10, 0)
		if err != nil || len(rows) != 1 {
			t.Fatalf("reservation missing before create: %+v %v", rows, err)
		}
		row := rows[0]
		ownedID = row.ID
		if row.Origin != sessioncatalog.Routine || row.OriginID != "rt" || row.CreationState != sessioncatalog.Creating || row.NativeID != "" {
			t.Fatalf("incomplete intent: %+v", row)
		}
		if req.Creation.Origin != "routine" || !req.Creation.Configured {
			t.Fatalf("owner intent not forwarded: %+v", req.Creation)
		}
	}
	linked, published := 0, 0
	svc.hooks.SessionCreated = func(info CreatedSession) {
		published++
		b, err := catalogue.Resolve(t.Context(), "owner", ownedID)
		if err != nil || b.NativeID != info.ID || info.RoutineID != "rt" {
			t.Fatalf("published before binding: %+v %v", b, err)
		}
	}
	link := func(string) error { linked++; return nil }
	req := identityRequest()
	resp, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "rt", link)
	if err != nil || resp.ID != "new-session" {
		t.Fatalf("create: %+v %v", resp, err)
	}
	// Port changes after a restart do not change creation intent.
	req.Port = "new-port"
	again, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "rt", link)
	if err != nil || again.ID != resp.ID || len(p.creates) != 1 || published != 1 || linked != 1 {
		t.Fatalf("replay repeated side effects: %+v %v creates=%d published=%d linked=%d", again, err, len(p.creates), published, linked)
	}
	if req.Creation.Origin != "user" {
		t.Fatal("caller creation metadata mutated")
	}
}

func TestCreateRetainsUncertainOutcomeWithoutLaunchingAgain(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	p.createErr = errors.New("connection lost after submission")
	_, err := svc.Create(t.Context(), "opencode", identityRequest())
	var uncertain *CreationUncertainError
	if !errors.As(err, &uncertain) || uncertain.OcmanID == "" {
		t.Fatalf("lost recovery identity: %v", err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.Creating {
		t.Fatalf("uncertain attempt discarded: %+v %v", rows, err)
	}
	p.createErr = nil
	if _, err := svc.Create(t.Context(), "opencode", identityRequest()); !errors.Is(err, sessioncatalog.ErrCreating) || len(p.creates) != 0 {
		t.Fatalf("uncertain create replayed: %v", err)
	}
}

func TestDefinitelyRejectedCreateDoesNotBlockExternalDiscovery(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	p.createErr = errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	if _, err := svc.Create(t.Context(), "opencode", identityRequest()); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("rejection retained creating reservation: %+v %v", rows, err)
	}
	result, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "external-after-rejection", Directory: "/repo"}})
	if err != nil || result.Deferred != 0 || result.Created != 1 {
		t.Fatalf("rejection poisoned discovery: %+v %v", result, err)
	}
}

func TestDefiniteConfigurationFailureIsNotRetried(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	p.ruleErr = errors.New("rules rejected")
	_, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{})
	if !errors.Is(err, p.ruleErr) {
		t.Fatal(err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("failure not retained: %+v %v", rows, err)
	}
	p.ruleErr = nil
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); !errors.Is(err, sessioncatalog.ErrCreationFailed) || len(p.creates) != 1 {
		t.Fatalf("failed creation replayed: %v", err)
	}
}

func TestCatalogueFailureNeverPublishesOrLosesNativeRecoveryID(t *testing.T) {
	svc, p, _, db := identityService(t)
	p.before = func(context.Context, platforms.CreateSessionRequest) { _ = db.Close() }
	svc.hooks.SessionCreated = func(CreatedSession) { t.Fatal("published unbound native session") }
	_, err := svc.Create(t.Context(), "opencode", identityRequest())
	var uncertain *CreationUncertainError
	if !errors.As(err, &uncertain) || uncertain.NativeID != "new-session" || uncertain.OcmanID == "" {
		t.Fatalf("missing recovery identities: %v", err)
	}
}

func TestCreationIntentRejectsChangedRulesAndPhysicalSource(t *testing.T) {
	svc, p, _, _ := identityService(t)
	rules := []platforms.PermissionRule{{Permission: "read", Action: "allow", Pattern: "*"}}
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), rules); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); !errors.Is(err, sessioncatalog.ErrRequestConflict) {
		t.Fatalf("changed rules accepted: %v", err)
	}
	p.sourceKey = "replacement-source"
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), rules); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
		t.Fatalf("source rebound: %v", err)
	}
	if len(p.creates) != 1 {
		t.Fatal("conflicting request created another execution")
	}
}

func TestForwardedConfigurationRunsOnOwnerBeforeBinding(t *testing.T) {
	svc, p, _, _ := identityService(t)
	req := identityRequest()
	req.Creation.Configured = true
	req.Creation.PermissionRules = []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}}
	if _, err := svc.Create(t.Context(), "opencode", req); err != nil {
		t.Fatal(err)
	}
	if len(p.rules) != 1 || len(p.rules[0].Rules) != 1 {
		t.Fatalf("owner did not apply forwarded rules: %+v", p.rules)
	}
}

func TestForwardedRoutineOriginIsPublishedOnWorktreePath(t *testing.T) {
	svc, _, _, _ := identityService(t)
	req := identityRequest()
	req.Creation.Origin, req.Creation.OriginID = "routine", "rt"
	var published CreatedSession
	svc.hooks.SessionCreated = func(info CreatedSession) { published = info }
	if _, err := svc.CreateConfigured(t.Context(), "opencode", req, []platforms.PermissionRule{}); err != nil {
		t.Fatal(err)
	}
	if published.RoutineID != "rt" {
		t.Fatalf("worktree published without origin: %+v", published)
	}
}

func TestSessionBindingFinishesAfterRequestCancellation(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.before = func(context.Context, platforms.CreateSessionRequest) { cancel() }
	resp, err := svc.Create(ctx, "opencode", identityRequest())
	if err != nil || resp.ID != "new-session" {
		t.Fatalf("accepted creation lost on disconnect: %+v %v", resp, err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.Ready {
		t.Fatalf("accepted creation not bound: %+v %v", rows, err)
	}
}

func TestCreationFailsClosedWithoutSourceOrCatalogue(t *testing.T) {
	for _, reason := range []string{"catalogue", "source", "source-key", "source-interface"} {
		t.Run(reason, func(t *testing.T) {
			svc, p, _, _ := identityService(t)
			switch reason {
			case "catalogue":
				svc.WithSessionCatalogue(nil, "owner")
			case "source":
				p.sourceErr = errors.New("source unavailable")
			case "source-key":
				p.sourceKey = ""
			case "source-interface":
				svc.registry = &fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": p.fakePlatform}}
			}
			if _, err := svc.Create(t.Context(), "opencode", identityRequest()); err == nil || len(p.creates) != 0 {
				t.Fatalf("creation bypassed %s: %v", reason, err)
			}
		})
	}
}

func TestRoutineIdentityMismatchCannotLaunch(t *testing.T) {
	svc, p, _, _ := identityService(t)
	req := identityRequest()
	req.Creation.Origin = "factory"
	req.Creation.OriginID = "attempt"
	if _, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "routine", nil); err == nil || len(p.creates) != 0 {
		t.Fatalf("mismatched origin accepted: %v", err)
	}
}

func TestUncertainConfigurationCleanupKeepsPendingIdentity(t *testing.T) {
	svc, p, catalogue, _ := identityService(t)
	p.ruleErr, p.disposeErr = errors.New("rules rejected"), errors.New("dispose failed")
	_, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{})
	var cleanup *ConfiguredSessionCleanupError
	var uncertain *CreationUncertainError
	if !errors.As(err, &cleanup) || !errors.As(err, &uncertain) || uncertain.NativeID != "new-session" {
		t.Fatalf("cleanup recovery identity lost: %v", err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].CreationState != sessioncatalog.Configuring {
		t.Fatalf("uncertain cleanup marked definitive: %+v %v", rows, err)
	}
	if rows[0].NativeID != "new-session" {
		t.Fatalf("native recovery ID not retained: %+v", rows[0])
	}
	p.ruleErr, p.disposeErr = nil, nil
	_, err = svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{})
	if !errors.As(err, &uncertain) || uncertain.NativeID != "new-session" || len(p.creates) != 1 {
		t.Fatalf("interrupted configuration relaunched: %v", err)
	}
}
