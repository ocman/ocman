package state

import (
	"database/sql"
	"testing"
)

func TestMigrateInboxPreservesExistingItems(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := migrateToV74(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO inbox_item (id, title, body, created_at, read_at, archived_at) VALUES ('old', 'Title', 'Body', 1, 2, 3)`); err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{94, 96, 96} {
		if err := applyMigration(tx, version); err != nil {
			t.Fatal(err)
		}
	}
	item, err := scanInboxItem(tx.QueryRow(`SELECT ` + inboxItemColumns + ` FROM inbox_item WHERE id = 'old'`))
	if err != nil || item.Category != InboxGeneral || item.Title != "Title" || item.ReadAt != 2 || item.ArchivedAt != 3 || item.Permission != nil || item.Session != nil {
		t.Fatalf("migrated item: %+v, %v", item, err)
	}
}

func TestInboxOriginSurvivesArchival(t *testing.T) {
	db, err := Open(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	origin := InboxSession{Platform: "opencode", SessionID: "ses-origin", Title: "Original title"}
	item, err := db.CreateSessionInboxItem(t.Context(), "Done", "Details", InboxGeneral, &origin)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecallInboxItem(t.Context(), item.ID); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].Session == nil || *items[0].Session != origin {
		t.Fatalf("archived origin: %+v, %v", items, err)
	}
	for _, invalid := range []InboxSession{{Platform: "opencode"}, {SessionID: "ses"}, {Platform: " ", SessionID: "ses"}} {
		if _, err := db.CreateSessionInboxItem(t.Context(), "Title", "Body", InboxGeneral, &invalid); err == nil {
			t.Fatalf("accepted incomplete origin: %+v", invalid)
		}
	}
	if err := db.NotifySessionInbox(t.Context(), "Delivered", "Details", InboxFactory, origin.Platform, origin.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := db.ResolvePermissionInboxItem(t.Context(), origin.Platform, origin.SessionID, "early-reply"); err != nil {
		t.Fatal(err)
	}
	items, err = db.ListArchivedInboxItems(t.Context())
	if err != nil || len(items) != 2 {
		t.Fatalf("resolved origin: %+v, %v", items, err)
	}
	for _, item := range items {
		if item.Session == nil || item.Session.SessionID != origin.SessionID {
			t.Fatalf("lost origin: %+v", item)
		}
	}
}

func TestRoutineInboxIncludesRunSession(t *testing.T) {
	db := openTestStateDB(t)
	defer db.Close()
	routine := testRoutine("routine", "Daily checks", 1)
	if err := db.CreateRoutine(t.Context(), routine); err != nil {
		t.Fatal(err)
	}
	_, claimed, err := db.ClaimRoutineRun(t.Context(), RoutineRun{ID: "run", RoutineID: routine.ID, State: "running", Trigger: "manual", OccurrenceAt: 2, CreatedAt: 2})
	if err != nil || !claimed {
		t.Fatalf("claim: %v, %v", claimed, err)
	}
	if err := db.LinkRoutineRun(t.Context(), "run", "r-laptop:opencode", "ses-run", 2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := db.FinishRoutineRun(t.Context(), "run", "success", "", 3, 0, false); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListInboxItems(t.Context())
	if err != nil || len(items) != 1 || items[0].Session == nil || items[0].Session.Platform != "r-laptop:opencode" || items[0].Session.SessionID != "ses-run" {
		t.Fatalf("run session: %+v, %v", items, err)
	}
}
