package server

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

func TestAutoApproveDefaultFollowsManagedSnapshot(t *testing.T) {
	if (&Server{autoApproveDefault: true}).AutoApproveDefault() != true {
		t.Fatal("no state db: flag ignored")
	}
	store := statetest.Open(t, ModuleSchemas()...)
	s := New(nil, store, "127.0.0.1:0", platforms.NewRegistry(), nil).WithAutoApproveDefault(true)
	if !s.AutoApproveDefault() {
		t.Fatal("unmanaged: flag ignored")
	}
	if err := store.EnableManagedSettings(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !s.AutoApproveDefault() {
		t.Fatal("managed before any hub: flag ignored")
	}
	if err := store.ApplyManagedSettings(t.Context(), state.ManagedSettings{Hub: "hub-1", AutoApproveDefault: false}, 1); err != nil {
		t.Fatal(err)
	}
	if s.AutoApproveDefault() {
		t.Fatal("hub default ignored")
	}
}

// The owner side of a hub's toggle stores it in the owner's database.
func TestServerSessionAutoApproveStoresOnOwner(t *testing.T) {
	store := statetest.Open(t, ModuleSchemas()...)
	s := New(nil, store, "127.0.0.1:0", platforms.NewRegistry(), nil)
	adapter := &fakePlatform{PlatformID: "opencode"}
	off := false
	got, err := s.SessionAutoApprove(t.Context(), adapter, "s1", &off)
	if err != nil || got.Enabled || !got.Overridden {
		t.Fatalf("state = %+v %v", got, err)
	}
	if enabled, exists, _ := store.GetAutoApprove(t.Context(), "opencode", "s1"); enabled || !exists {
		t.Fatal("toggle not stored on the owner")
	}
}
