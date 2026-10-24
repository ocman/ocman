package owned

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func TestPreparationReceiptMatchesSignatureUntilReferencesChange(t *testing.T) {
	db, err := state.Open(statetest.Path(t, sessioncatalog.Schema, ReferenceSchema), sessioncatalog.Schema, ReferenceSchema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewReferences(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if done, err := store.PreparationComplete(ctx, "owner", "/history", "v1"); err != nil || done {
		t.Fatalf("fresh receipt = %v, %v", done, err)
	}
	if err := store.CompletePreparation(ctx, "owner", "/history", "v1"); err != nil {
		t.Fatal(err)
	}
	if done, err := store.PreparationComplete(ctx, "owner", "/history", "v1"); err != nil || !done {
		t.Fatalf("completed receipt = %v, %v", done, err)
	}
	if err := store.CompletePreparation(ctx, "owner", "/history", "v2"); err != nil {
		t.Fatal(err)
	}
	if done, _ := store.PreparationComplete(ctx, "owner", "/history", "v1"); done {
		t.Fatal("a superseded signature still counts as complete")
	}
	if _, err := store.reserve(ctx, "test", "native"); err != nil {
		t.Fatal(err)
	}
	if found, err := store.HasResolvableLocalReferences(ctx, "owner", []string{"other", "test"}); err != nil || found {
		t.Fatalf("reference without a ready catalogue binding resolved: %v, %v", found, err)
	}
}
