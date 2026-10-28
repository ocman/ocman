package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

type identityInventoryNative struct{ *nativePlatform }

func (p *identityInventoryNative) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return nil, errors.New("migration requested expensive transcript aggregates")
}
func (p *identityInventoryNative) GetSessionIdentities(context.Context) ([]db.Session, error) {
	return p.rows, nil
}
func (p *identityInventoryNative) GetSessionLifecycle(context.Context, string) (db.SessionLifecycle, error) {
	return db.SessionLifecycle{}, errors.New("metadata inventory requested a lifecycle probe")
}

func TestStartupInventoryUsesScalarIdentityReader(t *testing.T) {
	a, p, store := fixture(t)
	a.native = &identityInventoryNative{p}
	if err := a.inventoryLegacy(t.Context(), nil, nil); err != nil {
		t.Fatal(err)
	}
	row, err := store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "source", NativeID: "native"})
	if err != nil || row.Directory != "/repo" || !sessioncatalog.IsID(row.ID) {
		t.Fatalf("identity metadata lost: %+v %v", row, err)
	}
}
