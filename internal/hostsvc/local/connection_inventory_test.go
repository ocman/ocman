package local

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

func TestDisableConnectionFencesAndDrainsBeforeChangingSavedIntent(t *testing.T) {
	h, store := connectionHost(t)
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	router := platformconnections.NewRouter(store)
	if err := router.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: "codex"}); err != nil {
		t.Fatal(err)
	}
	release, err := router.Begin(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	entered, nativeDrain, settled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h.deps.ConnectionLifecycle.Detach = func(ctx context.Context, actual platformconnections.Connection, disable func() error) error {
		if actual.ID != c.ID {
			t.Error("detaching sibling")
		}
		close(entered)
		return router.Detach(ctx, c.ID, func() error { close(nativeDrain); <-settled; return disable() })
	}
	done := make(chan error, 1)
	disabled := false
	go func() {
		done <- h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Enabled: &disabled})
	}()
	select {
	case err := <-done:
		t.Fatalf("disable bypassed drain: %v", err)
	case <-entered:
	}
	release()
	<-nativeDrain
	if _, err := router.Begin(t.Context(), c.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("new admission during native drain", err)
	}
	if current, err := store.Get(t.Context(), c.ID); err != nil || !current.Enabled {
		t.Fatal("disabled before native work/prompt settled", err)
	}
	close(settled)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if current, err := store.Get(t.Context(), c.ID); err != nil || current.Enabled {
		t.Fatal("disable intent not saved", err)
	}
}

func TestDisableConnectionWithoutDrainPolicyFailsClosed(t *testing.T) {
	h, store := connectionHost(t)
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Enabled: &disabled}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal("disabled without native drain policy", err)
	}
	if current, err := store.Get(t.Context(), c.ID); err != nil || !current.Enabled {
		t.Fatal("unsafe disabled intent saved", err)
	}
}

func TestDisabledConnectionCanBeRemovedBeforeCompositionRestart(t *testing.T) {
	h, store := connectionHost(t)
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "codex", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	router := platformconnections.NewRouter(store)
	if err := router.Bind(t.Context(), c.ID, &platformtest.Fake{PlatformID: "codex"}); err != nil {
		t.Fatal(err)
	}
	h.deps.ConnectionLifecycle.Detach = func(ctx context.Context, actual platformconnections.Connection, change func() error) error {
		return router.Detach(ctx, actual.ID, change)
	}
	disabled := false
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); err != nil {
		t.Fatalf("completed disable fence blocked tombstone: %v", err)
	}
	if current, err := store.Get(t.Context(), c.ID); err != nil || !current.Removed || current.Enabled {
		t.Fatalf("lost tombstone: %+v %v", current, err)
	}
	if _, err := router.Begin(t.Context(), c.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("second persistence reopened execution", err)
	}
}

func TestConnectionInventoryDoesNotInferReadiness(t *testing.T) {
	h, store := connectionHost(t)
	r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.SavePlatformConnection(t.Context(), r.ID, "Work", true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.InventoryPlatformConnections(t.Context())
	if err != nil || got.Supported {
		t.Fatalf("factory-less owner advertised support: %+v %v", got, err)
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Default: true}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
	current, _ := store.Get(t.Context(), c.ID)
	if current.Removed {
		t.Fatal("removed without drain policy")
	}
	h.deps.ConnectionLifecycle.Inspect = func(_ context.Context, actual platformconnections.Connection) (hostsvc.ConnectionCheck, error) {
		if actual.ID != c.ID || actual.Directory != c.Directory {
			t.Fatalf("inspected sibling %+v", actual)
		}
		return hostsvc.ConnectionCheck{Status: "ready", Account: "work"}, nil
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Default: true}); err != nil {
		t.Fatal(err)
	}
	got, err = h.InventoryPlatformConnections(t.Context())
	if err != nil || !got.Connections[0].IsDefault || got.Connections[0].Account != "work" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestConnectionRemovalRequiresSuccessfulDrain(t *testing.T) {
	h, store := connectionHost(t)
	r, _ := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "existing"})
	c, _ := h.SavePlatformConnection(t.Context(), r.ID, "Work", true)
	busy := errors.New("busy")
	h.deps.ConnectionLifecycle.Detach = func(context.Context, platformconnections.Connection, func() error) error { return busy }
	if err := h.RemovePlatformConnection(t.Context(), c.ID); !errors.Is(err, busy) {
		t.Fatal(err)
	}
	current, _ := store.Get(t.Context(), c.ID)
	if current.Removed {
		t.Fatal("busy connection removed")
	}
	h.deps.ConnectionLifecycle.Detach = func(_ context.Context, actual platformconnections.Connection, remove func() error) error {
		if actual.ID != c.ID {
			t.Fatal(actual)
		}
		return remove()
	}
	if err := h.RemovePlatformConnection(t.Context(), c.ID); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Get(t.Context(), c.ID)
	if !current.Removed {
		t.Fatal("connection not removed")
	}
}

func TestConnectionCheckUsesPinnedReceipt(t *testing.T) {
	h, _ := connectionHost(t)
	check, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{Kind: "codex", Configuration: "isolated"})
	if err != nil || check.ReservationID == "" || check.Status != "unchecked" {
		t.Fatalf("%+v %v", check, err)
	}
	again, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ReservationID: check.ReservationID})
	if err != nil || again.ReservationID != check.ReservationID || again.Directory != check.Directory {
		t.Fatalf("%+v %v", again, err)
	}
	if err := h.CancelPlatformConnectionSetup(t.Context(), check.ReservationID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ReservationID: check.ReservationID}); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestConnectionActivationRetriesSavedIdentity(t *testing.T) {
	h, store := connectionHost(t)
	r, err := h.PreparePlatformConnection(t.Context(), hostsvc.ConnectionSetupRequest{Kind: "codex", Configuration: "existing"})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	h.deps.ConnectionLifecycle.Activate = func(_ context.Context, c platformconnections.Connection) error {
		calls++
		if c.ID != r.ConnectionID || !c.Enabled {
			t.Fatalf("wrong activation %+v", c)
		}
		if calls == 1 {
			return errors.New("runtime unavailable")
		}
		return nil
	}
	c, err := h.SavePlatformConnection(t.Context(), r.ID, "Work", true)
	if err == nil || c.ID != r.ConnectionID {
		t.Fatalf("%+v %v", c, err)
	}
	again, err := h.SavePlatformConnection(t.Context(), r.ID, "Changed", true)
	if err != nil || again.ID != c.ID || again.Name != "Work" || calls != 2 {
		t.Fatalf("%+v %v calls=%d", again, err, calls)
	}
	collection, _ := store.Read(t.Context())
	if len(collection.Connections) != 1 {
		t.Fatal(collection)
	}
	if err := h.CancelPlatformConnectionSetup(t.Context(), r.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := store.Get(t.Context(), c.ID)
	if err != nil || kept.Removed {
		t.Fatal("dismissal removed a saved account", err)
	}
	no := false
	h.deps.ConnectionLifecycle.Detach = func(_ context.Context, actual platformconnections.Connection, disable func() error) error {
		if actual.ID != c.ID {
			t.Fatal("disable detached sibling")
		}
		return disable()
	}
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Enabled: &no}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("disable activated runtime")
	}
	yes := true
	if err := h.UpdatePlatformConnection(t.Context(), hostsvc.ConnectionUpdate{ID: c.ID, Enabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatal("enable did not activate exact connection")
	}
}
