package opencode

import (
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestRefreshSessionMissingRowEvictedWithoutInvalidation(t *testing.T) {
	resetSessionsCache()
	t.Cleanup(resetSessionsCache)
	store := newFakeSessionStore(dirtyFixture...)
	warmSnapshot(t, store)
	scans := store.fullScans.Load()

	store.remove("s-mid")
	if err := refreshSessionNow(t.Context(), store, "s-mid"); !errors.Is(err, db.ErrSessionNotFound) {
		t.Fatalf("refresh error = %v, want ErrSessionNotFound", err)
	}
	if _, err := refreshSessionsIncremental(t.Context(), store); err != nil {
		t.Fatalf("incremental refresh: %v", err)
	}
	for _, session := range currentSnapshot() {
		if session.ID == "s-mid" {
			t.Fatal("missing row remained in the snapshot")
		}
	}
	if got := store.fullScans.Load(); got != scans {
		t.Fatalf("full scans = %d, want %d", got, scans)
	}
}
