package sessionsvc

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/owned"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2/statev2test"
)

func TestCanonicalPlatformOwnsCreationWhileServiceLinksAndPublishesCanonicalID(t *testing.T) {
	svc, native, catalogue, _ := identityService(t)
	adapter, err := owned.New(t.Context(), native, catalogue, statev2test.Open(t), "owner")
	if err != nil {
		t.Fatal(err)
	}
	svc.registry = &fakeRegistry{byID: map[platforms.ID]platforms.Platform{adapter.ID(): adapter}}
	linked, published := 0, 0
	var linkedID string
	svc.hooks.SessionCreated = func(row CreatedSession) {
		published++
		if !sessioncatalog.IsID(row.ID) || row.ID != linkedID {
			t.Fatalf("publication used native or unlinked identity: %+v", row)
		}
		stored, err := catalogue.Get(t.Context(), row.ID)
		if err != nil || stored.CreationState != sessioncatalog.Ready {
			t.Fatalf("publication preceded commit: %+v %v", stored, err)
		}
	}
	link := func(id string) error {
		linked++
		linkedID = id
		if !sessioncatalog.IsID(id) {
			t.Fatalf("application linkage received native ID: %s", id)
		}
		stored, err := catalogue.Get(t.Context(), id)
		if err != nil || stored.CreationState != sessioncatalog.Configuring {
			t.Fatalf("linkage acquired ready execution too early: %+v %v", stored, err)
		}
		return nil
	}
	req := identityRequest()
	response, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "routine", link)
	if err != nil || response == nil || response.ID != linkedID || linked != 1 || published != 1 {
		t.Fatalf("canonical creation: %+v %d %d %v", response, linked, published, err)
	}
	if _, err := svc.CreateRoutine(t.Context(), "opencode", req, nil, "routine", link); err != nil || len(native.creates) != 1 || linked != 1 || published != 1 {
		t.Fatalf("creation replay repeated application work: %d %d %d %v", len(native.creates), linked, published, err)
	}
}
