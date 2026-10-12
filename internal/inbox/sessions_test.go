package inbox

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func TestSessionContextLocalTitleAndFallback(t *testing.T) {
	titles := map[string]string{"child": "Local child"}
	m := NewModule(Deps{SessionTitle: func(_ context.Context, id string) string { return titles[id] }})
	items := m.inboxSessionContext(t.Context(), "local", []state.InboxItem{
		{Permission: &state.InboxPermission{Platform: "opencode", SessionID: "child", Permission: "bash"}},
		{Session: &state.InboxSession{Platform: "opencode", SessionID: "gone", Title: "Stored title"}},
	})
	if items[0].Title != "Local child: Permission requested: bash" || items[1].Session.Title != "Stored title" {
		t.Fatalf("items = %+v", items)
	}
	m.SessionTitle = nil // no OpenCode database
	items = m.inboxSessionContext(t.Context(), "local", items)
	if items[0].Session.Title != "Local child" {
		t.Fatal("missing database lost stored title")
	}
}

func TestRunStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	NewModule(Deps{}).Run(ctx)
}

// A remote session's owner raises and resolves its own permission items.
func TestRemotePermissionsStayWithTheirOwner(t *testing.T) {
	m := NewModule(Deps{Broadcast: func(string, []byte) { t.Fatal("remote permission reached the hub's Inbox") }})
	m.NotifyPermission(state.InboxPermission{Platform: "r-laptop:opencode", SessionID: "s", PermissionID: "p"})
	m.ResolvePermission(t.Context(), "r-laptop:opencode", "s", "p")
}
