package opencode

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms/opencode/api"
)

func TestHistoryAdapterNeverNeedsLiveDiscovery(t *testing.T) {
	database := newTestDBWithSession(t, "session", "/repo")
	a := New(database, nil, nil, ocapi.Auth{})
	identity, err := a.HistoryIdentity(t.Context())
	if err != nil || identity.Platform != "opencode" || identity.SourceKey == "" {
		t.Fatalf("history identity: %+v %v", identity, err)
	}
	entries, err := a.HistoryInventory(t.Context())
	if err != nil || len(entries) != 1 || entries[0].ID != "session" {
		t.Fatalf("inventory: %+v %v", entries, err)
	}
	snapshot, err := a.HistorySnapshot(t.Context(), "session")
	if err != nil || snapshot.Session.ID != "session" {
		t.Fatalf("snapshot: %+v %v", snapshot, err)
	}
	a = New(nil, nil, nil, ocapi.Auth{})
	if _, err := a.HistoryIdentity(t.Context()); err == nil {
		t.Fatal("accepted missing source identity")
	}
	if _, err := a.HistoryInventory(t.Context()); err == nil {
		t.Fatal("accepted missing inventory")
	}
	if _, err := a.HistorySnapshot(t.Context(), "session"); err == nil {
		t.Fatal("accepted missing snapshot")
	}
}
