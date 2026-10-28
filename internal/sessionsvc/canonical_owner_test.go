package sessionsvc

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

type canonicalRemoteFake struct{ *fakePlatform }

func (*canonicalRemoteFake) CanonicalSessionIDs() bool { return true }

func TestCanonicalOwnerResolutionIgnoresConflictingReverseCache(t *testing.T) {
	svc, native, catalogue, _ := identityService(t)
	local, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	remote := &canonicalRemoteFake{fakePlatform: &fakePlatform{id: "r-other:opencode", available: true}}
	registry := platforms.NewRegistry()
	registry.Register(local)
	registry.Register(remote)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	svc.registry = registry
	created, err := svc.Create(t.Context(), "opencode", identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	registry.RememberSessions(remote.ID(), []db.Session{{ID: created.ID}})
	actual, err := svc.resolve(t.Context(), created.ID, "")
	if err != nil || actual != local {
		t.Fatalf("reverse cache rebound canonical authority: %+v %v", actual, err)
	}
}
