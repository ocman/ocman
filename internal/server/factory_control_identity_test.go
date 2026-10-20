package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

type factoryIdentityPlatform struct{ *fakePlatform }

func (p *factoryIdentityPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.ID()), SourceKey: "factory-source"}, nil
}

func TestFactoryControlReservesEpicProvenanceBeforeCreation(t *testing.T) {
	f := newControlFixture(t)
	f.srv.registry.Register(&factoryIdentityPlatform{f.platform})
	catalogue, err := sessioncatalog.New(f.srv.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	f.srv.sessions.WithSessionCatalogue(catalogue, "owner")
	control, created, err := f.srv.factoryModule().EnsureControlSession(t.Context(), "epic-1")
	if err != nil || !created {
		t.Fatalf("catalogue-backed control failed: %+v %v", control, err)
	}
	rows, err := catalogue.List(t.Context(), "owner", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].Origin != sessioncatalog.Factory || rows[0].OriginID != "epic-1" || rows[0].CreationState != sessioncatalog.Ready {
		t.Fatalf("missing control provenance: %+v %v", rows, err)
	}
}
