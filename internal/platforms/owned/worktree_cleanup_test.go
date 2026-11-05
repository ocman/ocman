package owned

import (
	"testing"

	"github.com/ocman/ocman/internal/statev2"
)

func TestWorktreeInventoryIncludesHiddenNativeChildren(t *testing.T) {
	a, _, parent := storedFixture(t)
	child, err := a.historyIdentity(t.Context(), "cleanup-child")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.history.Commit(t.Context(), statev2.Batch{Session: statev2.Session{Key: statev2.Key{InstanceID: "source", SessionID: child.ID}, ParentID: parent, Directory: "/stored", Title: "child", UpdatedAt: 20}}); err != nil {
		t.Fatal(err)
	}
	visible, err := a.Sessions(t.Context(), "", 0)
	if err != nil || len(visible) != 1 {
		t.Fatalf("display inventory changed: %+v %v", visible, err)
	}
	all, err := a.WorktreeSessions(t.Context())
	if err != nil || len(all) != 2 {
		t.Fatalf("maintenance inventory incomplete: %+v %v", all, err)
	}
	for _, row := range all {
		if row.ID == child.ID && (row.ParentID != parent || !row.SourceChild) {
			t.Fatalf("native hierarchy lost: %+v", row)
		}
	}
	if err := a.history.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.WorktreeSessions(t.Context()); err == nil {
		t.Fatal("maintenance inventory hid a read error")
	}
}
