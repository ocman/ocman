package owned

import (
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
	"github.com/ocman/ocman/internal/statev2"
)

func TestNamedSourceScopesReadsAndRetainsOfflineBrowsing(t *testing.T) {
	a, _, id := storedFixture(t)
	if _, err := a.store.Reconcile(t.Context(), sessioncatalog.Source{OwnerID: "owner", Platform: "test", SourceKey: "sibling"}, []sessioncatalog.NativeSession{{ID: "native", Directory: "/sibling"}}); err != nil {
		t.Fatal(err)
	}
	other, err := a.store.FindBinding(t.Context(), sessioncatalog.Binding{OwnerID: "owner", Platform: "test", SourceKey: "sibling", NativeID: "native"})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.history.RegisterInstance(t.Context(), statev2.Instance{ID: "sibling", OwnerID: "owner", Platform: "test", SourceKey: "sibling"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "sibling", SessionID: other.ID}, Directory: "/sibling", Title: "sibling", ReadFormat: statev2.ConversationReadFormat}}); err != nil {
		t.Fatal(err)
	}
	a.WithSourceScope()
	keys := a.ReadSourceKeys()
	if len(keys) != 1 || keys[0] != a.source.SourceKey {
		t.Fatal(keys)
	}
	keys[0] = "tampered"
	if a.ReadSourceKeys()[0] == "tampered" {
		t.Fatal("source getter exposed mutable state")
	}
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("cross-account aggregate: %+v %v", rows, err)
	}
	if a.Owns(t.Context(), other.ID) {
		t.Fatal("claimed sibling identity")
	}
	if _, err := a.SessionMetadata(t.Context(), other.ID); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	retained, err := NewRetained(a.store, a.history, "owner", a.source)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := retained.Session(t.Context(), id, 10, 0)
	if err != nil || detail == nil || len(detail.Messages) != 2 {
		t.Fatalf("lost retained read: %+v %v", detail, err)
	}
	if err := retained.ExecutionReady(t.Context()); err == nil {
		t.Fatal("retained adapter admitted execution")
	}
	if err := retained.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id}); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatal(err)
	}
	source, err := a.SidebarSource(t.Context())
	if err != nil || !source.Scoped {
		t.Fatalf("sidebar unscoped: %+v %v", source, err)
	}
	defer a.sidebarReader.Close()
	rows, err = a.SidebarSessions(t.Context(), platforms.SidebarQuery{Limit: 100})
	if err != nil || len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("sidebar crossed source: %+v %v", rows, err)
	}
}
