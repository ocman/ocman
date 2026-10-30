package state

import "testing"

func TestQueuePreserveSelectionsMigrationAndReads(t *testing.T) {
	d := openTestStateDB(t)
	if _, err := d.SQL().Exec(`ALTER TABLE queued_message DROP COLUMN preserve_selections`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().Exec(`INSERT INTO queued_message(id,platform,session_id,position,created_at) VALUES ('legacy','opencode','s',1,1)`); err != nil {
		t.Fatal(err)
	}
	tx, err := d.SQL().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := 0; i < 2; i++ {
		if err := migrateQueuePreserveSelections(tx); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	legacy, err := d.HeadQueuedMessage(t.Context(), "opencode", "s")
	if err != nil || legacy == nil || legacy.PreserveSelections {
		t.Fatalf("legacy queue selection policy changed: %+v %v", legacy, err)
	}
	if err := d.EnqueueMessage(t.Context(), QueuedMessage{ID: "preserved", Platform: "opencode", SessionID: "s", Text: "follow up", CreatedAt: 2, PreserveSelections: true}); err != nil {
		t.Fatal(err)
	}
	for _, read := range []func() ([]QueuedMessage, error){
		func() ([]QueuedMessage, error) { return d.ListQueuedMessages(t.Context(), "opencode", "s") },
		func() ([]QueuedMessage, error) { return d.ListQueuedMessagesAnyPlatform(t.Context(), "s") },
	} {
		rows, err := read()
		if err != nil || len(rows) != 2 || rows[0].PreserveSelections || !rows[1].PreserveSelections {
			t.Fatalf("selection mode lost in queue read: %+v %v", rows, err)
		}
	}
}

func TestQueueSelectionMigrationToleratesAbsentLegacyTable(t *testing.T) {
	d := openTestStateDB(t)
	if _, err := d.SQL().Exec(`DROP TABLE queued_message`); err != nil {
		t.Fatal(err)
	}
	tx, err := d.SQL().Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := migrateQueuePreserveSelections(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
