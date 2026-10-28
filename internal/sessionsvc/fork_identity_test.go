package sessionsvc

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type forkIdentityPlatform struct {
	*identityPlatform
	beforeFork func(platforms.ForkSessionRequest)
	forkErr    error
}

func (p *forkIdentityPlatform) ForkSession(ctx context.Context, req platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	if p.beforeFork != nil {
		p.beforeFork(req)
	}
	if p.forkErr != nil {
		return nil, p.forkErr
	}
	return p.fakePlatform.ForkSession(ctx, req)
}

func forkService(t *testing.T) (*Service, *forkIdentityPlatform, *sessioncatalog.Store, string) {
	t.Helper()
	svc, base, catalogue, _ := identityService(t)
	p := &forkIdentityPlatform{identityPlatform: base}
	svc = canonicalServiceWith(t, svc, p, catalogue)
	parent, err := svc.Create(t.Context(), "opencode", identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	return svc, p, catalogue, parent.ID
}

func forkRequest(parent, key string) platforms.ForkSessionRequest {
	return platforms.ForkSessionRequest{SessionID: parent, MessageID: "message", Creation: &platforms.CreationIdentity{RequestKey: key, Origin: "user"}}
}

func TestForkReservesChildAndParentBeforeNativeCopy(t *testing.T) {
	svc, p, catalogue, parent := forkService(t)
	p.beforeFork = func(req platforms.ForkSessionRequest) {
		if req.SessionID != "new-session" || req.MessageID != "message" {
			t.Fatalf("native fork arguments not translated: %+v", req)
		}
		child, err := catalogue.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "opencode", SourceKey: "source", NativeID: "forked-session"})
		if err == nil {
			t.Fatalf("child bound before native copy: %+v", child)
		}
	}
	req := forkRequest(parent, "fork")
	resp, err := svc.Fork(t.Context(), "opencode", req)
	if err != nil || !sessioncatalog.IsID(resp.ID) {
		t.Fatalf("fork: %+v %v", resp, err)
	}
	child, err := catalogue.Get(t.Context(), resp.ID)
	if err != nil || child.ParentID != parent || child.CreationState != sessioncatalog.Ready || child.NativeID != "forked-session" {
		t.Fatalf("child binding: %+v %v", child, err)
	}
	if again, err := svc.Fork(t.Context(), "opencode", req); err != nil || again.ID != resp.ID || len(p.forks) != 1 {
		t.Fatalf("fork replayed native copy: %v calls=%d", err, len(p.forks))
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
	svc, p, _, parent := forkService(t)
	p.forkErr = errors.New("connection lost")
	_, err := svc.Fork(t.Context(), "opencode", forkRequest(parent, "fork"))
	pendingID(t, err)
	p.forkErr = nil
	if _, err := svc.Fork(t.Context(), "opencode", forkRequest(parent, "fork")); !errors.Is(err, sessioncatalog.ErrCreating) || len(p.forks) != 0 {
		t.Fatalf("uncertain fork repeated: %v calls=%d", err, len(p.forks))
	}
}

func TestDefinitelyRejectedForkDoesNotBlockExternalDiscovery(t *testing.T) {
	svc, p, catalogue, parent := forkService(t)
	p.forkErr = errors.Join(platforms.ErrCreationRejected, platforms.ErrUnsupported)
	req := forkRequest(parent, "rejected-fork")
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, platforms.ErrCreationRejected) {
		t.Fatal(err)
	}
	result, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "opencode", SourceKey: "source"}, []sessioncatalog.NativeSession{{ID: "external-after-rejection", Directory: "/repo"}})
	if err != nil || result.Deferred != 0 || result.Created != 1 {
		t.Fatalf("rejection poisoned discovery: %+v %v", result, err)
	}
	if _, err := svc.Fork(t.Context(), "opencode", req); !errors.Is(err, sessioncatalog.ErrCreationFailed) {
		t.Fatalf("definite rejection replayed: %v", err)
	}
}

func TestForkRejectsWrongParentAndUnknownSession(t *testing.T) {
	svc, p, _, parent := forkService(t)
	req := forkRequest(parent, "fork")
	req.Creation.ParentID = "ocm_00000000-0000-0000-0000-000000000000"
	if _, err := svc.Fork(t.Context(), "opencode", req); err == nil {
		t.Fatal("wrong Ocman parent accepted")
	}
	if _, err := svc.Fork(t.Context(), "opencode", forkRequest("ocm_00000000-0000-0000-0000-000000000001", "unknown")); err == nil {
		t.Fatal("unknown parent accepted")
	}
	if _, err := svc.Fork(t.Context(), "opencode", forkRequest("native-parent", "native")); err == nil {
		t.Fatal("native parent reference accepted")
	}
	if len(p.forks) != 0 {
		t.Fatalf("invalid parent reached native fork: %+v", p.forks)
	}
}
