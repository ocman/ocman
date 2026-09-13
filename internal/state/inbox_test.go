package state

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestInboxMigration(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_version (version, applied_at) VALUES (73, 0);
		CREATE TABLE factory_plan_gate (epic_id TEXT PRIMARY KEY);
	`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := raw.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion)
	}
	if _, err := raw.Exec(`INSERT INTO inbox_item (id, title, body, created_at) VALUES ('id', 'title', 'body', 1)`); err != nil {
		t.Fatalf("inbox_item table unavailable after migration: %v", err)
	}
	var columns int
	if err := raw.QueryRow(`SELECT count(*) FROM pragma_table_info('factory_plan_gate') WHERE name = 'implementation_model'`).Scan(&columns); err != nil || columns != 1 {
		t.Fatalf("implementation_model columns = %d, %v", columns, err)
	}
}

func TestCreateInboxItemValidatesAndTrims(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	for _, input := range []struct{ title, body string }{{"", "body"}, {"title", " \n\t"}} {
		if _, err := db.CreateInboxItem(t.Context(), input.title, input.body); err == nil {
			t.Fatalf("CreateInboxItem(%q, %q) accepted blank input", input.title, input.body)
		}
	}

	item, err := db.CreateInboxItem(t.Context(), "  title  ", "\n body \t")
	if err != nil {
		t.Fatal(err)
	}
	if item.ID == "" || item.Title != "title" || item.Body != "body" || item.CreatedAt <= 0 {
		t.Fatalf("created item = %+v", item)
	}
	other, err := db.CreateInboxItem(t.Context(), "other", "body")
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == item.ID {
		t.Fatalf("opaque IDs collided: %q", item.ID)
	}
}

func TestInboxItemsPersistAndListNewestFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec(`
		INSERT INTO inbox_item (id, title, body, created_at) VALUES
			('older', 'Older', 'body', 100),
			('newer-a', 'Newer A', 'body', 200),
			('newer-b', 'Newer B', 'body', 200)
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	items, err := db.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 || items[0].ID != "newer-b" || items[1].ID != "newer-a" || items[2].ID != "older" {
		t.Fatalf("items = %+v, want deterministic newest-first order", items)
	}
}

func TestMarkInboxItemReadIsIdempotentAndIsolated(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if _, err := db.db.Exec(`
		INSERT INTO inbox_item (id, title, body, created_at, read_at) VALUES
			('read', 'Read', 'body', 1, 123),
			('unread', 'Unread', 'body', 2, NULL)
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInboxItemRead(t.Context(), "read"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInboxItemRead(t.Context(), "missing"); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if items[0].ID != "unread" || items[0].ReadAt != 0 || items[1].ReadAt != 123 {
		t.Fatalf("items after isolated no-op = %+v", items)
	}
	if err := db.MarkInboxItemRead(t.Context(), "unread"); err != nil {
		t.Fatal(err)
	}
	if err := db.MarkInboxItemRead(t.Context(), "unread"); err != nil {
		t.Fatal(err)
	}
	items, _ = db.ListInboxItems(t.Context())
	if items[0].ReadAt <= 0 {
		t.Fatalf("unread item was not marked read: %+v", items[0])
	}
	if count, err := db.CountUnreadInboxItems(t.Context()); err != nil || count != 0 {
		t.Fatalf("unread count = %d, %v; want 0", count, err)
	}
}

func TestRecallInboxItemIsIdempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if _, err := db.db.Exec(`INSERT INTO inbox_item (id, title, body, created_at) VALUES ('kept', 'Kept', 'body', 1), ('recalled', 'Recalled', 'body', 2)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"recalled", "recalled", "missing"} {
		if err := db.RecallInboxItem(t.Context(), id); err != nil {
			t.Fatalf("RecallInboxItem(%q): %v", id, err)
		}
	}
	items, err := db.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "kept" {
		t.Fatalf("active items = %+v, want only kept", items)
	}
	var archivedAt int64
	if err := db.db.QueryRow(`SELECT archived_at FROM inbox_item WHERE id = 'recalled'`).Scan(&archivedAt); err != nil || archivedAt <= 0 {
		t.Fatalf("recalled archived_at = %d, %v", archivedAt, err)
	}
}

func TestInboxItemIDsAreOwnerLocal(t *testing.T) {
	first, second := openTestStateDB(t), openTestStateDB(t)
	defer first.Close()
	defer second.Close()
	for _, db := range []*DB{first, second} {
		if _, err := db.db.Exec(`INSERT INTO inbox_item (id, title, body, created_at) VALUES ('same-id', 'Title', 'body', 1)`); err != nil {
			t.Fatal(err)
		}
	}
	if err := first.RecallInboxItem(t.Context(), "same-id"); err != nil {
		t.Fatal(err)
	}
	firstItems, _ := first.ListInboxItems(t.Context())
	secondItems, _ := second.ListInboxItems(t.Context())
	if len(firstItems) != 0 || len(secondItems) != 1 || secondItems[0].ID != "same-id" {
		t.Fatalf("first = %+v, second = %+v; mutation crossed owner stores", firstItems, secondItems)
	}
}

func TestArchiveInboxItemsAndAllRead(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if _, err := db.db.Exec(`
		INSERT INTO inbox_item (id, title, body, created_at, read_at) VALUES
			('selected', 'Selected', 'body', 1, NULL),
			('read', 'Read', 'body', 2, 20),
			('unread', 'Unread', 'body', 3, NULL)
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveInboxItems(t.Context(), []string{"selected", "missing"}); err != nil {
		t.Fatal(err)
	}
	if err := db.ArchiveAllReadInboxItems(t.Context()); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListInboxItems(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "unread" {
		t.Fatalf("active items = %+v, want only unread", items)
	}
	if count, err := db.CountUnreadInboxItems(t.Context()); err != nil || count != 1 {
		t.Fatalf("unread count = %d, %v; want 1", count, err)
	}
}

func TestWebhookDeliveryAcceptanceIsIdempotent(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if err := db.SaveWebhookInbox(t.Context(), WebhookInbox{ID: "inbox", RoutineID: "routine", RelayURL: "https://relay", Identity: "identity"}); err != nil {
		t.Fatal(err)
	}
	inboxes, err := db.ListWebhookInboxes(t.Context())
	if err != nil || len(inboxes) != 1 || inboxes[0].ID != "inbox" {
		t.Fatalf("inboxes = %+v, %v", inboxes, err)
	}
	if err := db.RecordWebhookIgnored(t.Context(), "inbox", "ignored", 5); err != nil {
		t.Fatal(err)
	}
	claimed, err := db.ClaimWebhookDispatch(t.Context(), "inbox", "dispatched", "routine", 6)
	if err != nil || !claimed {
		t.Fatalf("first dispatch claim = %v, %v", claimed, err)
	}
	claimed, err = db.ClaimWebhookDispatch(t.Context(), "inbox", "dispatched", "routine", 7)
	if err != nil || claimed {
		t.Fatalf("duplicate dispatch claim = %v, %v", claimed, err)
	}
	if err := db.FinishWebhookDispatch(t.Context(), "inbox", "dispatched", "routine", "success", "", 8); err != nil {
		t.Fatal(err)
	}
	counts, err := db.WebhookDispatchCounts(t.Context(), "inbox")
	if err != nil || counts["ignored"] != 1 || counts["success"] != 1 {
		t.Fatalf("dispatch counts = %+v, %v", counts, err)
	}
	accepted, err := db.AcceptWebhookDelivery(t.Context(), "inbox", "delivery", "POST webhook", "body", 10)
	if err != nil || !accepted {
		t.Fatalf("first acceptance = %v, %v", accepted, err)
	}
	accepted, err = db.AcceptWebhookDelivery(t.Context(), "inbox", "delivery", "POST webhook", "body", 10)
	if err != nil || accepted {
		t.Fatalf("duplicate acceptance = %v, %v", accepted, err)
	}
	items, err := db.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 {
		t.Fatalf("items = %v, %v", items, err)
	}
}

func TestWebhookDeliveryErrorBackoffIsBounded(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	if err := db.SaveWebhookInbox(t.Context(), WebhookInbox{ID: "inbox", RoutineID: "routine", RelayURL: "https://relay", Identity: "identity"}); err != nil {
		t.Fatal(err)
	}
	now := time.UnixMilli(1000)
	for i := 0; i < 8; i++ {
		if err := db.RecordWebhookDeliveryError(t.Context(), "inbox", "delivery", "bad ciphertext", now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Hour)
	}
	allowed, err := db.WebhookDeliveryRetryAllowed(t.Context(), "inbox", "delivery", now)
	if err != nil || allowed {
		t.Fatalf("retry after max attempts = %v, %v", allowed, err)
	}
}

func TestWebhookStoreReportsClosedDatabase(t *testing.T) {
	db := openTestStateDB(t)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	tests := map[string]func() error{
		"cleanup": func() error { return db.CleanupWebhookHistory(ctx, 1) },
		"save sub": func() error {
			return db.SaveWebhookSubscription(ctx, WebhookSubscription{ID: "sub", InboxID: "inbox", RoutineID: "routine"})
		},
		"list subs":  func() error { _, err := db.ListWebhookSubscriptions(ctx, "inbox"); return err },
		"delete sub": func() error { return db.DeleteWebhookSubscription(ctx, "inbox", "routine") },
		"claim":      func() error { _, err := db.ClaimWebhookDispatch(ctx, "inbox", "delivery", "routine", 1); return err },
		"finish": func() error {
			return db.FinishWebhookDispatch(ctx, "inbox", "delivery", "routine", "failure", "error", 1)
		},
		"ignore": func() error { return db.RecordWebhookIgnored(ctx, "inbox", "delivery", 1) },
		"save inbox": func() error {
			return db.SaveWebhookInbox(ctx, WebhookInbox{ID: "inbox", RoutineID: "routine", RelayURL: "https://relay", Identity: "identity"})
		},
		"get inbox":    func() error { _, err := db.GetWebhookInbox(ctx, "routine"); return err },
		"list inboxes": func() error { _, err := db.ListWebhookInboxes(ctx); return err },
		"delete inbox": func() error { return db.DeleteWebhookInbox(ctx, "routine") },
		"counts":       func() error { _, err := db.WebhookDispatchCounts(ctx, "inbox"); return err },
		"accept": func() error {
			_, err := db.AcceptWebhookDelivery(ctx, "inbox", "delivery", "title", "body", 1)
			return err
		},
		"record error": func() error { return db.RecordWebhookDeliveryError(ctx, "inbox", "delivery", "error", time.Now()) },
		"retry": func() error {
			_, err := db.WebhookDeliveryRetryAllowed(ctx, "inbox", "delivery", time.Now())
			return err
		},
	}
	for name, call := range tests {
		t.Run(name, func(t *testing.T) {
			if err := call(); err == nil {
				t.Fatal("operation succeeded on closed database")
			}
		})
	}
}
