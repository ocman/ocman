package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
	"github.com/ocman/ocman/internal/state/statetest"
)

type lifecycleNative struct {
	*platformtest.Fake
	inspection platforms.ConnectionInspection
	err        error
	busy       bool
	checked    chan struct{}
	once       sync.Once
}

func TestNativeUnreadySiblingRemainsInspectableInOwnerInventory(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	var keys []platforms.ID
	var native []platforms.Integration
	for _, ready := range []bool{false, true} {
		c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "Account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, platforms.ID(c.ID))
		native = append(native, &lifecycleNative{Fake: &platformtest.Fake{PlatformID: "claudecode"}, inspection: platforms.ConnectionInspection{Ready: ready}})
	}
	lifecycle := connectionLifecycle(connectionFactory{state: core}, platformconnections.NewRouter(store), keys, native)
	host := local.New(local.Deps{ConnectionStore: store, ConnectionLifecycle: lifecycle})
	defer host.Finalize()
	inventory, err := host.InventoryPlatformConnections(t.Context())
	if err != nil || len(inventory.Connections) != 2 {
		t.Fatalf("unready sibling broke inventory: %+v %v", inventory, err)
	}
	if inventory.Connections[0].Status != "sign-in-required" || inventory.Connections[1].Status != "ready" {
		t.Fatal(inventory.Connections)
	}
	check, err := host.CheckPlatformConnection(t.Context(), hostsvc.ConnectionCheckRequest{ID: string(keys[0])})
	if err != nil || check.Status != "sign-in-required" {
		t.Fatalf("unready account check failed: %+v %v", check, err)
	}
}

func (p *lifecycleNative) InspectConnection(context.Context) (platforms.ConnectionInspection, error) {
	return p.inspection, p.err
}
func (p *lifecycleNative) ReloadIdle(context.Context) (bool, error) {
	if p.checked != nil {
		p.once.Do(func() { close(p.checked) })
	}
	return !p.busy, p.err
}

func TestConnectionLifecycleNativeDrainCancellationRestoresAdmission(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	store := platformconnections.New(core)
	if _, err := store.Initialize(t.Context(), platformconnections.Seed{}); err != nil {
		t.Fatal(err)
	}
	c, err := store.Create(t.Context(), platformconnections.Input{Kind: "claudecode", Name: "account", Configuration: "isolated", Directory: t.TempDir(), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	p := &lifecycleNative{Fake: &platformtest.Fake{PlatformID: "claudecode"}, busy: true, checked: make(chan struct{}), inspection: platforms.ConnectionInspection{Ready: true, AccountLabel: "account"}}
	router := platformconnections.NewRouter(store)
	if err := router.Bind(t.Context(), c.ID, p); err != nil {
		t.Fatal(err)
	}
	lifecycle := connectionLifecycle(connectionFactory{state: core}, router, []platforms.ID{platforms.ID(c.ID)}, []platforms.Integration{p})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- lifecycle.Detach(ctx, c, func() error { t.Error("removed active turn/prompt"); return nil })
	}()
	<-p.checked
	if _, err := router.Begin(t.Context(), c.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal("admitted while native drain active", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	release, err := router.Begin(t.Context(), c.ID)
	if err != nil {
		t.Fatal("failed drain retained fence", err)
	}
	release()
	p.busy = false
	if err := lifecycle.Detach(t.Context(), c, func() error { return store.Remove(t.Context(), c.ID) }); err != nil {
		t.Fatal(err)
	}
	if _, err := router.Begin(t.Context(), c.ID); !errors.Is(err, platformconnections.ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestConnectionLifecycleInspectionUsesNativeAccountNotAvailability(t *testing.T) {
	core := statetest.Open(t, platformconnections.Schema)
	c := platformconnections.Connection{ID: "cnx_test", Kind: "claudecode", Directory: t.TempDir()}
	p := &lifecycleNative{Fake: &platformtest.Fake{PlatformID: "claudecode"}, inspection: platforms.ConnectionInspection{Ready: true, AccountLabel: "Work", Message: "Native identity verified"}}
	lifecycle := connectionLifecycle(connectionFactory{state: core}, platformconnections.NewRouter(platformconnections.New(core)), []platforms.ID{platforms.ID(c.ID)}, []platforms.Integration{p})
	result, err := lifecycle.Inspect(t.Context(), c)
	if err != nil || result.Status != "ready" || result.Account != "Work" {
		t.Fatalf("actual account lost: %+v %v", result, err)
	}
	p.inspection.Ready = false
	result, err = lifecycle.Inspect(t.Context(), c)
	if err != nil || result.Status != "sign-in-required" {
		t.Fatalf("Available became readiness: %+v %v", result, err)
	}
	p.err = errors.New("native inspection failed")
	result, err = lifecycle.Inspect(t.Context(), c)
	if err == nil || result.Status != "error" {
		t.Fatalf("inspection failed open: %+v %v", result, err)
	}
	unknown := c
	unknown.ID = "cnx_missing"
	unknown.Kind = "unknown"
	result, err = lifecycle.Inspect(t.Context(), unknown)
	if err != nil || result.Status != "unchecked" {
		t.Fatalf("unknown claimed ready: %+v %v", result, err)
	}
}
