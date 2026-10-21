package owned

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type driftingMigrationPlatform struct{ *nativePlatform }

func (p *driftingMigrationPlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	p.source = "changed-after-inventory-start"
	return p.rows, nil
}

func TestPrepareCoreFailsClosedBeforeRekeyWhenActiveSourceChanges(t *testing.T) {
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
	if err := core.PinSession(t.Context(), "test", "native"); err != nil {
		t.Fatal(err)
	}
	p := &driftingMigrationPlatform{nativePlatform: &nativePlatform{id: "test", source: "source", rows: []db.Session{{ID: "native"}}}}
	if adapters, err := PrepareCore(t.Context(), core, history, catalogue, "owner", []platforms.Integration{p}); !errors.Is(err, platforms.ErrHistoryUnavailable) || adapters != nil {
		t.Fatalf("drifting source activated: %+v %v", adapters, err)
	}
	refs, err := core.SessionReferences(t.Context(), "test")
	if err != nil || len(refs) != 1 || refs[0] != "native" {
		t.Fatalf("failed inventory changed state: %+v %v", refs, err)
	}
	if rows, err := catalogue.List(t.Context(), "owner", 500, 0); err != nil || len(rows) != 0 {
		t.Fatalf("drifting source acquired execution bindings: %+v %v", rows, err)
	}
}

func TestPrepareCoreRejectsInvalidComposition(t *testing.T) {
	core, _, remote, _ := restoreFixture(t)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	history, err := statev2.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	if _, err := PrepareCore(t.Context(), nil, history, catalogue, "owner", nil); !errors.Is(err, sessioncatalog.ErrInvalid) {
		t.Fatalf("missing core accepted: %v", err)
	}
	canonical, _, _ := fixture(t)
	for _, adapter := range []platforms.Integration{nil, remote, canonical} {
		if adapters, err := PrepareCore(t.Context(), core, history, catalogue, "owner", []platforms.Integration{adapter}); err == nil || adapters != nil {
			t.Fatalf("invalid preparation input accepted: %T %v", adapter, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if adapters, err := PrepareCore(ctx, core, history, catalogue, "owner", nil); !errors.Is(err, context.Canceled) || adapters != nil {
		t.Fatalf("cancelled migration activated: %+v %v", adapters, err)
	}
}
