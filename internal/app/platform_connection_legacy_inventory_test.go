package app

import (
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestMigratedInventoryUsesLegacyAvailabilityOnlyForProvenance(t *testing.T) {
	for _, kind := range []string{"claudecode", "codex", "opencode"} {
		t.Run(kind, func(t *testing.T) {
			core := statetest.Open(t, platformconnections.Schema)
			store := platformconnections.New(core)
			if _, err := store.Initialize(t.Context(), platformconnections.Seed{ExistingInstallation: true, Connections: []platformconnections.Input{{Kind: kind, Name: "Machine", Configuration: "existing", Directory: t.TempDir(), Enabled: true}}, DefaultKind: kind}); err != nil {
				t.Fatal(err)
			}
			c, err := store.Legacy(t.Context(), kind)
			if err != nil {
				t.Fatal(err)
			}
			available := true
			p := &lifecycleNative{Fake: &platformtest.Fake{PlatformID: kind, Availability: &available}, inspection: platforms.ConnectionInspection{Message: "legacy cannot use strict inspection"}}
			lifecycle := connectionLifecycle(connectionFactory{state: core}, platformconnections.NewRouter(store), []platforms.ID{platforms.ID(c.ID)}, []platforms.Integration{p})
			h := local.New(local.Deps{ConnectionStore: store, ConnectionLifecycle: lifecycle})
			defer h.Finalize()
			got, err := h.InventoryPlatformConnections(t.Context())
			if err != nil || len(got.Connections) != 1 || got.Connections[0].Status != "ready" {
				t.Fatalf("migrated inventory=%+v err=%v", got, err)
			}
			if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Default: true}); err != nil {
				t.Fatalf("working legacy default rejected: %v", err)
			}
			available = false
			check, err := lifecycle.Inspect(t.Context(), c)
			if err != nil || check.Status == "ready" {
				t.Fatalf("unavailable legacy marked ready: %+v %v", check, err)
			}
			available = true
			c.LegacyKind = ""
			check, err = lifecycle.Inspect(t.Context(), c)
			if err != nil || check.Status == "ready" {
				t.Fatalf("new account bypassed strict inspection: %+v %v", check, err)
			}
			c.LegacyKind = "different-kind"
			check, err = lifecycle.Inspect(t.Context(), c)
			if err != nil || check.Status == "ready" {
				t.Fatalf("mismatched provenance bypassed inspection: %+v %v", check, err)
			}
		})
	}
}
