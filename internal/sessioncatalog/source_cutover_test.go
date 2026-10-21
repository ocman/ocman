package sessioncatalog

import (
	"context"
	"errors"
	"testing"
)

func TestSourceCutoverIsAtomicReplaySafeAndSourceScoped(t *testing.T) {
	store, core := openStore(t)
	source := Source{OwnerID: "owner", Platform: "test", SourceKey: "first"}
	if _, err := store.Reconcile(t.Context(), source, []NativeSession{{ID: "legacy", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	// A failed metadata update must not commit the cutover marker.
	if _, err := core.SQL().Exec(`CREATE TRIGGER reject_cutover BEFORE UPDATE OF legacy_keys ON session_catalog BEGIN SELECT RAISE(ABORT,'test rollback'); END`); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainSourceLegacyKeys(t.Context(), source); err == nil {
		t.Fatal("failed source update committed boundary")
	}
	if _, err := core.SQL().Exec(`DROP TRIGGER reject_cutover`); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainSourceLegacyKeys(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Reconcile(t.Context(), source, []NativeSession{{ID: "modern", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainSourceLegacyKeys(t.Context(), source); err != nil {
		t.Fatal(err)
	}
	for _, native := range []string{"legacy", "modern"} {
		row, err := store.FindBinding(t.Context(), Binding{OwnerID: "owner", Platform: "test", SourceKey: "first", NativeID: native})
		if err != nil || row.LegacyKeys != (native == "legacy") {
			t.Fatalf("source boundary changed %s key policy: %+v %v", native, row, err)
		}
	}
	other := source
	other.SourceKey = "second"
	if _, err := store.Reconcile(t.Context(), other, []NativeSession{{ID: "legacy", Directory: "/repo"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetainSourceLegacyKeys(t.Context(), other); err != nil {
		t.Fatal(err)
	}
	row, err := store.FindBinding(t.Context(), Binding{OwnerID: "owner", Platform: "test", SourceKey: "second", NativeID: "legacy"})
	if err != nil || !row.LegacyKeys {
		t.Fatalf("another physical source inherited completed cutover: %+v %v", row, err)
	}
	if err := store.RetainSourceLegacyKeys(t.Context(), Source{}); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.RetainSourceLegacyKeys(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
