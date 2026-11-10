package local

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestConnectionInventoryRejectsInvalidInspectionAndMutationBoundaries(t *testing.T) {
	h, store := connectionHost(t)
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	name := "Renamed"
	enabled := true
	for _, req := range []hostsvc.ConnectionUpdate{{ID: c.ID}, {ID: c.ID, Name: &name, Enabled: &enabled}} {
		if err := h.UpdatePlatformConnection(t.Context(), req); !errors.Is(err, platformconnections.ErrInvalid) {
			t.Fatal("partial multi-field update admitted", err)
		}
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Name: &name}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Get(t.Context(), c.ID); err != nil || got.Name != name {
		t.Fatal(got, err)
	}
	for _, mode := range []string{"native-error", "invalid-status"} {
		t.Run(mode, func(t *testing.T) {
			h.deps.ConnectionLifecycle.Inspect = func(context.Context, platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
				if mode == "native-error" {
					return hostsvc.ConnectionCheck{}, errors.New("native unavailable")
				}
				return hostsvc.ConnectionCheck{Status: "unknown-status"}, nil
			}
			inventory, inventoryErr := h.InventoryPlatformConnections(t.Context())
			check, checkErr := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ID: c.ID})
			if mode == "native-error" {
				if inventoryErr != nil || len(inventory.Connections) != 1 || inventory.Connections[0].Status != "error" || checkErr != nil || check.Status != "error" {
					t.Fatalf("broken row hidden: %+v %v %+v %v", inventory, inventoryErr, check, checkErr)
				}
			} else if inventoryErr == nil || checkErr == nil {
				t.Fatal("invalid native inspection status accepted")
			}
			if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Default: true}); err == nil {
				t.Fatal("invalid inspection became default")
			}
		})
	}
	if _, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ID: "missing"}); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal(err)
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: "missing", Name: &name}); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal(err)
	}
	h.deps.ConnectionLifecycle.Detach = func(_ context.Context, _ platformconnections.Connection, remove func() error) error { return remove() }
	if err := h.RemovePlatformConnection(t.Context(), "missing"); !errors.Is(err, platformconnections.ErrNotFound) {
		t.Fatal(err)
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); err != nil {
		t.Fatal("tombstone remove not idempotent", err)
	}
	if _, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ID: c.ID}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Name: &name}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestConnectionChecksUseOnlyOwnerPublishedExistingDirectory(t *testing.T) {
	h, _ := connectionHost(t)
	dir := h.deps.ConnectionConfigurations["codex"].ExistingDirectory
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	h.deps.ConnectionLifecycle.Inspect = func(_ context.Context, c platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		if c.Directory != dir {
			t.Fatal("inspected browser supplied path")
		}
		return hostsvc.ConnectionCheck{Status: "sign-in-required"}, nil
	}
	for _, req := range []hostsvc.ConnectionCheckRequest{{Kind: "unknown", Configuration: "existing", Directory: dir}, {Kind: "codex", Configuration: "isolated", Directory: dir}, {Kind: "codex", Configuration: "existing", Directory: t.TempDir()}} {
		if _, err := h.CheckPlatformConnection(t.Context(), req); !errors.Is(err, platformconnections.ErrInvalid) {
			t.Fatal("untrusted configuration accepted", err)
		}
	}
	check, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{Kind: "codex", Configuration: "existing", Directory: dir})
	if err != nil || check.ReservationID == "" || check.Status != "sign-in-required" || check.Directory != dir {
		t.Fatalf("owner reservation failed: %+v %v", check, err)
	}
}

func TestConnectionInventoryUnsupportedAndStorageFailuresFailClosed(t *testing.T) {
	h := New(Deps{})
	if _, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	if err := h.CompletePlatformConnectionOnboarding(t.Context()); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	h = New(Deps{ConnectionStore: store, ConnectionLifecycle: hostsvc.ConnectionLifecycle{Inspect: func(context.Context, platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		return hostsvc.ConnectionCheck{Status: "ready"}, nil
	}}})
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.InventoryPlatformConnections(t.Context()); err == nil {
		t.Fatal("unreadable collection became empty")
	}
}
