package owned

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type unavailableSource struct{ platforms.Platform }

func (*unavailableSource) ID() platforms.ID               { return "unavailable" }
func (*unavailableSource) Available(context.Context) bool { return false }
func (*unavailableSource) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	return platforms.HistoryIdentity{}, platforms.ErrHistoryUnavailable
}

func TestUnavailableSelectedPlatformDoesNotBlockLocalMigrationOrAcquireExecution(t *testing.T) {
	core, _, _, _ := restoreFixture(t)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if err := core.PinSession(t.Context(), "unavailable", "native"); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareCore(t.Context(), core, history, catalogue, "owner", []platforms.Integration{&unavailableSource{}})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("unavailable source blocked startup: %+v %v", prepared, err)
	}
	refs, err := core.SessionReferences(t.Context(), "unavailable")
	if err != nil || len(refs) != 1 || !identity.IsPendingReference(refs[0]) {
		t.Fatalf("unavailable source acquired execution identity: %+v %v", refs, err)
	}
	a := prepared[0].(*Adapter)
	if _, err := a.nativeID(t.Context(), "ocm_00000000-0000-4000-8000-000000000001"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("unavailable adapter allowed execution resolution: %v", err)
	}
}
