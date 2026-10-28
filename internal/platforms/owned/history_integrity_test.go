package owned

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestCanonicalHistoryRejectsForeignSnapshotContents(t *testing.T) {
	for _, kind := range []string{"missing snapshot", "wrong session", "wrong message owner", "wrong part owner"} {
		t.Run(kind, func(t *testing.T) {
			a, p := historyFixture(t)
			snapshot := p.snapshot
			switch kind {
			case "missing snapshot":
				snapshot = nil
			case "wrong session":
				snapshot.Session.ID = "foreign"
			case "wrong message owner":
				snapshot.Messages[0].SessionID = "foreign"
			case "wrong part owner":
				snapshot.Parts[0].SessionID = "foreign"
			}
			if _, err := a.historySnapshot(t.Context(), "hidden-child", snapshot); !errors.Is(err, sessioncatalog.ErrInvalid) {
				t.Fatalf("foreign history accepted: %v", err)
			}
		})
	}
}

func TestRuntimeRecoveryRejectsUnknownOwnersAndUnsupportedNativeReaders(t *testing.T) {
	a, p, store := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetSessionIdentities(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("invented runtime inventory: %v", err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), row.ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("invented runtime evidence: %v", err)
	}
	_, err = store.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "other", Platform: "test", SourceKey: "foreign"}, []sessioncatalog.NativeSession{{ID: "native"}})
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "other", Platform: "test", SourceKey: "foreign", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), foreign.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("runtime evidence crossed owner: %v", err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), "ocm_00000000-0000-4000-8000-000000000099"); !errors.Is(err, sessioncatalog.ErrNotFound) {
		t.Fatalf("unknown recovery identity: %v", err)
	}
	p.source = "changed"
	if _, err := a.GetSessionIdentities(t.Context()); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := a.GetSessionLifecycle(t.Context(), row.ID); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	if _, err := a.historySnapshot(t.Context(), "native", &db.HistorySnapshot{Session: db.Session{ID: "native"}}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("history import crossed source: %v", err)
	}
}
