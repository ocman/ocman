package sessionsvc

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type namedRemoteIdentity struct {
	*canonicalRemoteFake
	info   platforms.SessionIdentity
	probes int
}

func (p *namedRemoteIdentity) NativeKind() platforms.ID { return "opencode" }
func (p *namedRemoteIdentity) SessionOwnerID() string   { return "remote" }
func (p *namedRemoteIdentity) SessionIdentity(context.Context, platforms.SessionIdentityRequest) (*platforms.SessionIdentity, error) {
	p.probes++
	copy := p.info
	return &copy, nil
}
func (p *namedRemoteIdentity) SessionIdentities(context.Context, []string) ([]platforms.SessionIdentity, error) {
	return []platforms.SessionIdentity{p.info}, nil
}

func addNamedRemote(t *testing.T, r connectionRig, id string) *namedRemoteIdentity {
	t.Helper()
	key := platforms.ID(platforms.CompoundPlatformID("remote", "cnx_"+uuid.NewString()))
	p := &namedRemoteIdentity{
		canonicalRemoteFake: &canonicalRemoteFake{fakePlatform: &fakePlatform{id: key, available: true, createResp: &platforms.CreateSessionResponse{ID: id}}},
		info:                platforms.SessionIdentity{ID: id, OwnerID: "remote", Platform: "opencode", SourceKey: "remote-source", Origin: "user", CreationState: "ready", CreatedAt: 1, UpdatedAt: 2},
	}
	registry := r.svc.registry.(*platforms.Registry)
	if err := registry.RegisterConnection(key, p); err != nil {
		t.Fatal(err)
	}
	registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "remote" && kind == "opencode" && source == p.info.SourceKey {
			return key, nil
		}
		return "", platforms.ErrNotFound
	})
	return p
}

func TestNamedRemoteDraftCreationDoesNotRequireLocalReceipt(t *testing.T) {
	r := newConnectionRig(t)
	id := "ocm_" + uuid.NewString()
	remote := addNamedRemote(t, r, id)
	request := identityRequest()
	request.Creation.SessionID = id
	response, err := r.svc.Create(t.Context(), string(remote.ID()), request)
	if err != nil || response.ID != id || len(remote.creates) != 1 {
		t.Fatalf("remote draft was blocked by owner-local admission: %+v %v", response, err)
	}
}

func TestUncachedNamedRemoteMutationAuthenticatesAndMirrorsIdentity(t *testing.T) {
	r := newConnectionRig(t)
	id := "ocm_" + uuid.NewString()
	remote := addNamedRemote(t, r, id)
	if err := r.svc.Abort(t.Context(), string(remote.ID()), platforms.AbortRequest{SessionID: id}); err != nil {
		t.Fatalf("uncached remote session bypassed authenticated discovery: %v", err)
	}
	row, err := r.catalogue.GetCanonical(t.Context(), id)
	if err != nil || row.OwnerID != "remote" || row.Platform != "opencode" || remote.probes != 1 || len(remote.aborts) != 1 {
		t.Fatalf("remote identity not authenticated/mirrored: %+v %v probes=%d", row, err, remote.probes)
	}
}

func TestOmittedScopeRemoteLookupUsesExactSourceAmongSameKindAccounts(t *testing.T) {
	r := newConnectionRig(t)
	first := addNamedRemote(t, r, "ocm_"+uuid.NewString())
	first.info.SourceKey = "remote-first-source"
	second := addNamedRemote(t, r, "ocm_"+uuid.NewString())
	second.info.SourceKey = "remote-second-source"
	if err := r.catalogue.MirrorCanonical(t.Context(), sessioncatalog.Session{
		ID: second.info.ID, OwnerID: "remote", Platform: "opencode", SourceKey: second.info.SourceKey,
		Origin: sessioncatalog.User, CreationState: sessioncatalog.Ready, CreatedAt: 1, UpdatedAt: 2,
	}); err != nil {
		t.Fatal(err)
	}
	info, err := r.svc.LookupSessionIdentity(t.Context(), "", platforms.SessionIdentityRequest{ID: second.info.ID})
	if err != nil || info.Platform != string(second.ID()) || first.probes != 0 || second.probes != 1 {
		t.Fatalf("lookup guessed a same-kind remote sibling: %+v %v first=%d second=%d", info, err, first.probes, second.probes)
	}
}
