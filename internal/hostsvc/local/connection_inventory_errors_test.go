package local

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestInventoryRetainsBrokenAccountAndHealthySibling(t *testing.T) {
	h, store := connectionHost(t)
	var ids []string
	for _, enabled := range []bool{false, true} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: enabled})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, c.ID)
	}
	h.deps.ConnectionLifecycle.Inspect = func(_ context.Context, c platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		if c.ID == ids[0] {
			return hostsvc.ConnectionCheck{}, errors.New("secret diagnostic must stay owner-local")
		}
		return hostsvc.ConnectionCheck{Status: "ready", Account: "Healthy"}, nil
	}
	got, err := h.InventoryPlatformConnections(t.Context())
	if err != nil || !got.Supported || len(got.Connections) != 2 {
		t.Fatalf("inventory=%+v err=%v", got, err)
	}
	if got.Connections[0].Status != "error" || got.Connections[1].Status != "ready" {
		t.Fatalf("rows=%+v", got.Connections)
	}
	check, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ID: ids[0]})
	if err != nil || check.Status != "error" || strings.Contains(check.Instructions, "secret") || check.Directory == "" {
		t.Fatalf("check=%+v err=%v", check, err)
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: ids[0], Default: true}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatalf("broken default accepted: %v", err)
	}
}

func TestInventoryPreservesCancellationAndStorageFailures(t *testing.T) {
	h, store := connectionHost(t)
	if _, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	h.deps.ConnectionLifecycle.Inspect = func(context.Context, platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		cancel()
		return hostsvc.ConnectionCheck{}, context.Canceled
	}
	if _, err := h.InventoryPlatformConnections(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	h.deps.ConnectionLifecycle.Inspect = func(context.Context, platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		return hostsvc.ConnectionCheck{}, sql.ErrConnDone
	}
	if _, err := h.InventoryPlatformConnections(t.Context()); !errors.Is(err, sql.ErrConnDone) {
		t.Fatalf("storage inspection failure hidden: %v", err)
	}
	core := statetest.Open(t, platformconnections.Schema)
	h.deps.ConnectionStore = platformconnections.New(core)
	if err := core.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := h.InventoryPlatformConnections(t.Context()); err == nil {
		t.Fatal("unreadable collection reported as successful inventory")
	}
}
