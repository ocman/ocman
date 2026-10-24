package state

import (
	"strings"
	"testing"
)

func TestFactoryActionInboxLifecycle(t *testing.T) {
	db := openTestStateDB(t)
	ctx := t.Context()
	actions := map[string]string{"review:1": "Review plan\n\n[Review](/factory/epics/ship)"}
	sync := func(actions map[string]string) []string {
		t.Helper()
		added, err := db.SyncFactoryActionInbox(ctx, "ship", "Factory needs attention: Ship", actions)
		if err != nil {
			t.Fatal(err)
		}
		return added
	}
	if added := sync(actions); len(added) != 1 || added[0] != actions["review:1"] {
		t.Fatalf("first sync added = %q", added)
	}
	if added := sync(actions); len(added) != 0 {
		t.Fatalf("repeat sync added = %q", added)
	}
	items, err := db.ListInboxItems(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %#v, %v; want one action", items, err)
	}
	item := items[0]
	if item.Category != InboxFactory || !strings.HasPrefix(item.ID, "factory-action-") || item.Body != actions["review:1"] {
		t.Fatalf("item = %#v", item)
	}
	if err := db.MarkInboxItemRead(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	sync(actions)
	items, _ = db.ListInboxItems(ctx)
	if len(items) != 1 || items[0].ReadAt == 0 {
		t.Fatal("refresh reset read state")
	}
	sync(nil)
	items, _ = db.ListInboxItems(ctx)
	if len(items) != 0 {
		t.Fatal("resolved action remains active")
	}
	sync(actions)
	items, _ = db.ListInboxItems(ctx)
	if len(items) != 0 {
		t.Fatal("stale snapshot resurrected resolved action")
	}
	sync(map[string]string{"review:2": "Review revision 2"})
	items, _ = db.ListInboxItems(ctx)
	if len(items) != 1 || items[0].ID == item.ID {
		t.Fatal("new revision did not create new action")
	}
}
