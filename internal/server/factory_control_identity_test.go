package server

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/owned"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2/statev2test"
)

type factoryIdentityPlatform struct{ *fakePlatform }

func (p *factoryIdentityPlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{Platform: string(p.ID()), SourceKey: "factory-source"}, nil
}

func TestFactoryControlReservesEpicProvenanceBeforeCreation(t *testing.T) {
	f := newControlFixture(t)
	catalogue, err := sessioncatalog.New(f.srv.stateDB)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := owned.New(t.Context(), &factoryIdentityPlatform{f.platform}, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	registry.Register(adapter)
	if err := registry.EnableCanonicalSessions(); err != nil {
		t.Fatal(err)
	}
	f.srv.registry = registry
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
