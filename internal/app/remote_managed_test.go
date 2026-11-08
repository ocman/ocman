package app

import (
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/remote"
	"github.com/ocman/ocman/internal/server"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestValidateRemoteManaged(t *testing.T) {
	if err := validateRemoteManaged("", true); err == nil {
		t.Fatal("managed without a listener accepted")
	}
	for _, c := range []struct {
		listen  string
		managed bool
	}{{"", false}, {"0.0.0.0:8230", false}, {"0.0.0.0:8230", true}} {
		if err := validateRemoteManaged(c.listen, c.managed); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

func TestEnableRemoteManaged(t *testing.T) {
	store := statetest.Open(t)
	if err := enableRemoteManaged(t.Context(), store, false); err != nil || store.ManagedMode() {
		t.Fatalf("unmanaged: %v %v", err, store.ManagedMode())
	}
	if err := enableRemoteManaged(t.Context(), store, true); err != nil || !store.ManagedMode() {
		t.Fatalf("managed: %v", err)
	}
	if err := store.ApplyManagedSettings(t.Context(), state.ManagedSettings{Hub: "hub-1"}, 1); err != nil {
		t.Fatal(err)
	}
	// Restart with a saved snapshot.
	if err := enableRemoteManaged(t.Context(), store, true); err != nil {
		t.Fatal(err)
	}
	if got, ok := store.ManagedSettings(); !ok || got.Hub != "hub-1" {
		t.Fatalf("reloaded snapshot = %+v", got)
	}
	_ = store.Close()
	if err := enableRemoteManaged(t.Context(), store, true); err == nil {
		t.Fatal("closed store accepted")
	}
}

// The hub sends its effective settings, including the -auto-approve default,
// and a local settings change triggers a resend.
func TestUseHubManagedSettings(t *testing.T) {
	store := statetest.Open(t)
	registry := platforms.NewRegistry()
	srv := server.New(nil, store, "127.0.0.1:0", registry, nil).WithAutoApproveDefault(true)
	mgr := remote.NewManager(registry, hostsvc.NewRouter(nil), store, "opencode")
	t.Cleanup(mgr.Stop)
	useHubManagedSettings(mgr, srv, store, "hub-1")
	if err := store.SetJudgeDelayMs(t.Context(), 300); err != nil {
		t.Fatal(err)
	}
	got, err := hubManagedSettings(srv, store, "hub-1")(t.Context())
	if err != nil || got.Hub != "hub-1" || !got.AutoApproveDefault || got.JudgeDelayMs != 300 {
		t.Fatalf("snapshot = %+v %v", got, err)
	}
}
