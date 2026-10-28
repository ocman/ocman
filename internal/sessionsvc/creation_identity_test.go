package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
	"github.com/ocman/ocman/internal/statev2/statev2test"
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
	db := statetest.Open(t, sessioncatalog.Schema)
	catalogue, err := sessioncatalog.New(db)
	if err != nil {
		t.Fatal(err)
	}
	p := &identityPlatform{fakePlatform: &fakePlatform{id: "opencode", available: true}, sourceKey: "source"}
	svc := New(&fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}, Hooks{})
	svc.WithSessionCatalogue(catalogue, "owner")
	return svc, p, catalogue, db
}

// canonicalIdentityService routes through the production canonical adapter
// and a canonical registry, as every registered local owner does after cutover.
func canonicalIdentityService(t *testing.T) (*Service, *identityPlatform, *sessioncatalog.Store, *state.DB) {
	t.Helper()
	svc, p, catalogue, db := identityService(t)
	return canonicalServiceWith(t, svc, p, catalogue), p, catalogue, db
}

func canonicalServiceWith(t *testing.T, svc *Service, native platforms.Integration, catalogue *sessioncatalog.Store) *Service {
	t.Helper()
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	svc.registry = registry
	return svc
}

func identityRequest() platforms.CreateSessionRequest {
	return platforms.CreateSessionRequest{Directory: "/repo", Title: "test", Creation: &platforms.CreationIdentity{RequestKey: "start", Origin: "user"}}
}

func onlyRow(t *testing.T, catalogue *sessioncatalog.Store) sessioncatalog.Session {
	t.Helper()
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 {
		t.Fatalf("want one creation intent: %+v %v", rows, err)
	}
	return rows[0]
}

func pendingID(t *testing.T, err error) string {
	t.Helper()
	var pending *platforms.CreationPendingError
	if !errors.As(err, &pending) || !sessioncatalog.IsID(pending.SessionID) {
		t.Fatalf("lost canonical recovery identity: %v", err)
	}
	return pending.SessionID
}

func TestCreateReservesRoutineIdentityBeforeNativeSideEffects(t *testing.T) {
	svc, p, catalogue, _ := canonicalIdentityService(t)
	p.before = func(ctx context.Context, req platforms.CreateSessionRequest) {
		row := onlyRow(t, catalogue)
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
		if row := onlyRow(t, catalogue); row.ID != info.ID || row.CreationState != sessioncatalog.Ready || info.RoutineID != "rt" {
			t.Fatalf("published before binding: %+v %+v", row, info)
		}
	}
	link := func(string) error { linked++; return nil }
	req := identityRequest()
	resp, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "rt", link)
	if err != nil || !sessioncatalog.IsID(resp.ID) {
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
	svc, p, catalogue, _ := canonicalIdentityService(t)
	p.createErr = errors.New("connection lost after submission")
	_, err := svc.Create(t.Context(), "opencode", identityRequest())
	id := pendingID(t, err)
	if row := onlyRow(t, catalogue); row.ID != id || row.CreationState != sessioncatalog.Creating {
		t.Fatalf("uncertain attempt discarded: %+v", row)
	}
	p.createErr = nil
	if _, err := svc.Create(t.Context(), "opencode", identityRequest()); !errors.Is(err, sessioncatalog.ErrCreating) || len(p.creates) != 0 {
		t.Fatalf("uncertain create replayed: %v", err)
	}
}

func TestDefinitelyRejectedCreateDoesNotBlockExternalDiscovery(t *testing.T) {
	svc, p, catalogue, _ := canonicalIdentityService(t)
	p.createErr = errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	if _, err := svc.Create(t.Context(), "opencode", identityRequest()); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	if row := onlyRow(t, catalogue); row.CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("rejection retained creating reservation: %+v", row)
	}
	result, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "external-after-rejection", Directory: "/repo"}})
	if err != nil || result.Deferred != 0 || result.Created != 1 {
		t.Fatalf("rejection poisoned discovery: %+v %v", result, err)
	}
}

func TestDefiniteConfigurationFailureIsNotRetried(t *testing.T) {
	svc, p, catalogue, _ := canonicalIdentityService(t)
	p.ruleErr = errors.New("rules rejected")
	svc.hooks.SessionCreated = func(CreatedSession) { t.Fatal("published unconfigured session") }
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); !errors.Is(err, p.ruleErr) {
		t.Fatal(err)
	}
	if row := onlyRow(t, catalogue); row.CreationState != sessioncatalog.CreationFailed {
		t.Fatalf("failure not retained: %+v", row)
	}
	p.ruleErr = nil
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); !errors.Is(err, sessioncatalog.ErrCreationFailed) || len(p.creates) != 1 {
		t.Fatalf("failed creation replayed: %v", err)
	}
}

func TestCatalogueFailureNeverPublishesOrLosesRecoveryID(t *testing.T) {
	svc, p, _, db := canonicalIdentityService(t)
	p.before = func(context.Context, platforms.CreateSessionRequest) { _ = db.Close() }
	svc.hooks.SessionCreated = func(CreatedSession) { t.Fatal("published unbound native session") }
	_, err := svc.Create(t.Context(), "opencode", identityRequest())
	pendingID(t, err)
}

func TestCreationIntentRejectsChangedRulesAndPhysicalSource(t *testing.T) {
	svc, p, _, _ := canonicalIdentityService(t)
	rules := []platforms.PermissionRule{{Permission: "read", Action: "allow", Pattern: "*"}}
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), rules); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); !errors.Is(err, sessioncatalog.ErrRequestConflict) {
		t.Fatalf("changed rules accepted: %v", err)
	}
	p.sourceKey = "replacement-source"
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), rules); err == nil {
		t.Fatal("replacement source reused another source's receipt")
	}
	if len(p.creates) != 1 {
		t.Fatal("conflicting request created another execution")
	}
}

func TestForwardedConfigurationRunsOnOwnerBeforeBinding(t *testing.T) {
	svc, p, _, _ := canonicalIdentityService(t)
	req := identityRequest()
	req.Creation.Configured = true
	req.Creation.PermissionRules = []platforms.PermissionRule{{Permission: "read", Pattern: "*", Action: "allow"}}
	resp, err := svc.Create(t.Context(), "opencode", req)
	if err != nil || !resp.ConfigurationApplied {
		t.Fatalf("forwarded configuration not reported: %+v %v", resp, err)
	}
	if len(p.rules) != 1 || len(p.rules[0].Rules) != 1 || p.rules[0].SessionID != "new-session" {
		t.Fatalf("owner did not apply forwarded rules natively: %+v", p.rules)
	}
}

func TestForwardedRoutineOriginIsPublishedOnWorktreePath(t *testing.T) {
	svc, _, _, _ := canonicalIdentityService(t)
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
	svc, p, catalogue, _ := canonicalIdentityService(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p.before = func(context.Context, platforms.CreateSessionRequest) { cancel() }
	resp, err := svc.Create(ctx, "opencode", identityRequest())
	if err != nil || !sessioncatalog.IsID(resp.ID) {
		t.Fatalf("accepted creation lost on disconnect: %+v %v", resp, err)
	}
	if row := onlyRow(t, catalogue); row.ID != resp.ID || row.CreationState != sessioncatalog.Ready {
		t.Fatalf("accepted creation not bound: %+v", row)
	}
}

func TestCreationFailsClosedWithoutPhysicalSource(t *testing.T) {
	for _, reason := range []string{"source", "source-key"} {
		t.Run(reason, func(t *testing.T) {
			svc, p, _, _ := canonicalIdentityService(t)
			switch reason {
			case "source":
				p.sourceErr = errors.New("source unavailable")
			case "source-key":
				p.sourceKey = ""
			}
			if _, err := svc.Create(t.Context(), "opencode", identityRequest()); err == nil || len(p.creates) != 0 {
				t.Fatalf("creation bypassed %s: %v", reason, err)
			}
		})
	}
}

func TestRoutineIdentityMismatchCannotLaunch(t *testing.T) {
	svc, p, _, _ := canonicalIdentityService(t)
	req := identityRequest()
	req.Creation.Origin = "factory"
	req.Creation.OriginID = "attempt"
	if _, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "routine", nil); err == nil || len(p.creates) != 0 {
		t.Fatalf("mismatched origin accepted: %v", err)
	}
}

func TestUncertainConfigurationCleanupKeepsPendingIdentity(t *testing.T) {
	svc, p, catalogue, _ := canonicalIdentityService(t)
	p.ruleErr, p.disposeErr = errors.New("rules rejected"), errors.New("dispose failed")
	_, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{})
	id := pendingID(t, err)
	row := onlyRow(t, catalogue)
	if row.ID != id || row.CreationState != sessioncatalog.Configuring || row.NativeID != "new-session" {
		t.Fatalf("uncertain cleanup lost the retained native binding: %+v", row)
	}
	p.ruleErr, p.disposeErr = nil, nil
	if _, err := svc.CreateConfigured(t.Context(), "opencode", identityRequest(), []platforms.PermissionRule{}); pendingID(t, err) != id || len(p.creates) != 1 {
		t.Fatalf("uncertain cleanup replayed native creation: %v creates=%d", err, len(p.creates))
	}
}
