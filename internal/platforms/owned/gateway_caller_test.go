package owned

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func callerGateway(t *testing.T) (*IdentityGateway, *Adapter, *sessioncatalog.Store, string) {
	t.Helper()
	a, _, store := fixture(t)
	row, err := a.canonical(t.Context(), "native")
	if err != nil {
		t.Fatal(err)
	}
	registry := platforms.NewRegistry()
	if err := registry.RegisterConnection("cnx_work", a); err != nil {
		t.Fatal(err)
	}
	registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		if owner == "owner" && kind == "test" && source == "source" {
			return "cnx_work", nil
		}
		return "", platforms.ErrNotFound
	})
	g := NewIdentityGateway(store, "owner", func() IdentityRegistry { return registry }, nil, nil)
	return g, a, store, row.ID
}

func TestLocalCallerReadsOnlyUniqueDurableIdentity(t *testing.T) {
	g, _, store, id := callerGateway(t)
	for _, ref := range []string{"native", id} {
		row, err := g.LocalCaller(t.Context(), "test", ref)
		if err != nil || row.ID != id || row.Platform != "cnx_work" || row.Directory != "/repo" {
			t.Fatalf("caller %s = %+v, %v", ref, row, err)
		}
	}
	// A duplicate native ID across sources must not pick the current account.
	if _, err := store.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "other"}, []sessioncatalog.NativeSession{{ID: "native", Directory: "/other"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LocalCaller(t.Context(), "test", "native"); !errors.Is(err, sessioncatalog.ErrBindingConflict) {
		t.Fatalf("ambiguous native caller accepted: %v", err)
	}
	if _, err := g.LocalCaller(t.Context(), "test", id); err != nil {
		t.Fatalf("canonical caller became ambiguous: %v", err)
	}
	if err := store.Scrub(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "source"}, "native"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.LocalCaller(t.Context(), "test", id); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("purged caller accepted: %v", err)
	}
}

func TestLocalCallerFailsClosed(t *testing.T) {
	for _, scenario := range []string{"unknown", "wrong kind", "wrong owner", "pending", "disconnected", "wrong source"} {
		t.Run(scenario, func(t *testing.T) {
			g, a, store, id := callerGateway(t)
			kind := "test"
			switch scenario {
			case "unknown":
				id = "not-recorded"
			case "wrong kind":
				kind = "opencode"
			case "wrong owner":
				g.owner = "other"
			case "pending":
				row, _, err := store.Reserve(t.Context(), sessioncatalog.Reservation{OwnerID: "owner", Platform: "test", Directory: "/repo", Origin: sessioncatalog.User, RequestKey: "pending", PayloadHash: strings.Repeat("a", 64)})
				if err != nil {
					t.Fatal(err)
				}
				id = row.ID
			case "disconnected":
				g.registry().(*platforms.Registry).Unregister("cnx_work")
			case "wrong source":
				a.source.SourceKey = "different"
			}
			if row, err := g.LocalCaller(t.Context(), kind, id); err == nil || row != nil {
				t.Fatalf("unproven caller accepted: %+v, %v", row, err)
			}
		})
	}
	var missing *IdentityGateway
	if _, err := missing.LocalCaller(t.Context(), "test", "native"); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
}
