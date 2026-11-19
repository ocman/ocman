package owned

import (
	"testing"

	"github.com/ocman/ocman/internal/sessioncatalog"
)

// Session summaries cross the remote boundary, so they must carry the owner's
// catalogue origin; the hub may not have mirrored the identity.
func TestStoredSummariesCarryCatalogueOrigin(t *testing.T) {
	a, _, id := storedFixture(t)
	identity, err := a.store.Get(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	source := sessioncatalog.Source{OwnerID: identity.OwnerID, Platform: identity.Platform, SourceKey: identity.SourceKey}
	if _, err := a.store.Reconcile(t.Context(), source, []sessioncatalog.NativeSession{{ID: identity.NativeID, Title: identity.Title, Directory: identity.Directory, Origin: sessioncatalog.Plugin, OriginID: "pentest"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || rows[0].Origin != "plugin" || rows[0].OriginID != "pentest" {
		t.Fatalf("summary origin = %+v %v", rows, err)
	}
	row, err := a.SessionSummary(t.Context(), id)
	if err != nil || row.Origin != "plugin" || row.OriginID != "pentest" {
		t.Fatalf("single summary origin = %+v %v", row, err)
	}
}
