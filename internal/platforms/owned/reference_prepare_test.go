package owned

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms/identity"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func TestFeatureReferencePreparationRequiresUniqueOwnerProof(t *testing.T) {
	core, refs, _, _ := restoreFixture(t)
	catalogue, err := sessioncatalog.New(core)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"active", "inactive"} {
		if _, err := catalogue.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: source}, []sessioncatalog.NativeSession{{ID: source + "-unique"}, {ID: "collision"}}); err != nil {
			t.Fatal(err)
		}
	}
	id, err := refs.PrepareReference(t.Context(), catalogue, "owner", "test", "active-unique")
	if err != nil || !sessioncatalog.IsID(id) {
		t.Fatalf("unique reference: %q %v", id, err)
	}
	row, err := catalogue.Get(t.Context(), id)
	if err != nil || !row.LegacyKeys {
		t.Fatalf("feature dedup provenance lost: %+v %v", row, err)
	}
	for _, tc := range []struct{ scope, reference string }{
		{"test", "collision"}, {"r-remote:test", "active-unique"}, {"", "active-unique"}, {"test", "unknown"},
	} {
		id, err := refs.PrepareReference(t.Context(), catalogue, "owner", tc.scope, tc.reference)
		if err != nil || !identity.IsPendingReference(id) {
			t.Fatalf("unproven reference gained identity: %+v %q %v", tc, id, err)
		}
		if replay, err := refs.PrepareReference(t.Context(), catalogue, "owner", tc.scope, id); err != nil || replay != id {
			t.Fatalf("quarantine replay: %q %v", replay, err)
		}
		if _, err := refs.PrepareReference(t.Context(), catalogue, "owner", "wrong-scope", id); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
			t.Fatalf("foreign scope adopted token: %v", err)
		}
	}
}
