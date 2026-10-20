package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type forkIdentityPlatform struct {
	*identityPlatform
	beforeFork   func(platforms.ForkSessionRequest)
	forkErr      error
	nilFork      bool
	summaryErr   error
	summaryReads int
}

func (p *forkIdentityPlatform) SessionSummary(_ context.Context, id string) (*db.Session, error) {
	p.summaryReads++
	if p.summaryErr != nil {
		return nil, p.summaryErr
	}
	return &db.Session{ID: id, Directory: "/parent", Title: "external parent"}, nil
}

func (p *forkIdentityPlatform) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	if p.beforeFork != nil {
		p.beforeFork(req)
	}
	if p.forkErr != nil {
		return nil, p.forkErr
	}
	if p.nilFork {
		return nil, nil
	}
	return p.fakePlatform.ForkSession(ctx, req)
}

func forkService(t *testing.T) (*Service, *forkIdentityPlatform, *sessioncatalog.Store) {
	t.Helper()
	svc, base, catalogue, _ := identityService(t)
	p := &forkIdentityPlatform{identityPlatform: base}
	svc.registry = &fakeRegistry{byID: map[platforms.ID]platforms.Platform{p.ID(): p}}
	return svc, p, catalogue
}

func TestForkReservesChildAndParentBeforeNativeCopy(t *testing.T) {
	svc, p, catalogue := forkService(t)
	var parentID string
	p.beforeFork = func(req platforms.ForkSessionRequest) {
		if req.SessionID != "parent" || req.MessageID != "message" {
			t.Fatalf("native fork arguments rewritten: %+v", req)
		}
		parentID = req.Creation.ParentID
		rows, err := catalogue.List(t.Context(), "owner", 10, 0)
		if err != nil || len(rows) != 2 {
			t.Fatalf("identity allocation occurred after native fork: %+v %v", rows, err)
		}
		found := false
		for _, row := range rows {
			if row.CreationState == sessioncatalog.Creating {
				found = true
				if row.ParentID != parentID || row.Directory != "/parent" || row.Origin != sessioncatalog.User || row.NativeID != "" {
					t.Fatalf("child intent incomplete: %+v", row)
				}
			}
		}
		if !found {
			t.Fatal("no reserved child")
		}
	}
	req := platforms.ForkSessionRequest{SessionID: "parent", MessageID: "message", Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user"}}
	resp, err := svc.Fork(t.Context(), "opencode", req)
	if err != nil || resp.ID != "forked-session" {
		t.Fatalf("fork: %+v %v", resp, err)
	}
	child, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: resp.ID})
	if err != nil || child.ParentID != parentID || child.CreationState != sessioncatalog.Ready {
		t.Fatalf("child binding: %+v %v", child, err)
	}
	if _, err := svc.Fork(t.Context(), "opencode", req); err != nil || len(p.forks) != 1 || p.summaryReads != 1 {
		t.Fatalf("fork replayed native copy: %v calls=%d summaries=%d", err, len(p.forks), p.summaryReads)
	}
	req.MessageID = "different-message"
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, sessioncatalog.ErrRequestConflict) {
		t.Fatalf("changed fork point accepted: %v", err)
	}
	if req.Creation.ParentID != "" {
		t.Fatal("caller metadata mutated")
	}
}

func TestForkUnknownOutcomeRetainsIdentityAndBlocksReplay(t *testing.T) {
	svc, p, _ := forkService(t)
	p.forkErr = errors.New("connection lost")
	req := platforms.ForkSessionRequest{SessionID: "parent", Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user"}}
	_, err := svc.Fork(t.Context(), "opencode", req)
	var uncertain *CreationUncertainError
	if !errors.As(err, &uncertain) || uncertain.OcmanID == "" {
		t.Fatalf("uncertain fork lost identity: %v", err)
	}
	p.forkErr = nil
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, sessioncatalog.ErrCreating) || len(p.forks) != 0 {
		t.Fatalf("uncertain fork repeated: %v", err)
	}
}

func TestDefinitelyRejectedForkDoesNotBlockExternalDiscovery(t *testing.T) {
	svc, p, catalogue := forkService(t)
	p.forkErr = errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	req := platforms.ForkSessionRequest{SessionID: "parent", MessageID: "unsupported-message", Creation: &platforms.CreationIdentity{RequestKey: "rejected-fork", Origin: "user"}}
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	result, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "external-after-rejection", Directory: "/parent"}})
	if err != nil || result.Deferred != 0 || result.Created != 1 {
		t.Fatalf("rejection poisoned discovery: %+v %v", result, err)
	}
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, sessioncatalog.ErrCreationFailed) {
		t.Fatalf("definite rejection replayed: %v", err)
	}
}

func TestForkRejectsWrongParentAndUnavailableSummary(t *testing.T) {
	svc, p, _ := forkService(t)
	req := platforms.ForkSessionRequest{SessionID: "parent", Creation: &platforms.CreationIdentity{RequestKey: "fork", Origin: "user", ParentID: "wrong-parent"}}
	if _, err := svc.Fork(t.Context(), "opencode", req); err == nil {
		t.Fatal("wrong Ocman parent accepted")
	}
	req.SessionID = "unknown"
	req.Creation.ParentID = ""
	p.summaryErr = platforms.ErrNotFound
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("unknown native parent accepted: %v", err)
	}
	if len(p.forks) != 0 {
		t.Fatal("rejected fork reached native owner")
	}
}
