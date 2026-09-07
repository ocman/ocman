package state

import (
	"database/sql"
	"path/filepath"
	"testing"
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
