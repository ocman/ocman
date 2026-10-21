package statev2

import (
	"path/filepath"
	"testing"
)

func TestAttentionProjectionTracksSnapshotCorrectionsAndStream(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Messages = []Message{{ID: "u", Role: "user", CreatedAt: 100, ConversationVisible: true}, {ID: "a", Role: "assistant", CreatedAt: 110, CompletedAt: 120, ConversationVisible: true, Position: 1}, {ID: "hidden", Role: "user", CreatedAt: 999}}
	b.Parts = nil
	commit(t, d, b)
	assert := func(completed, prompt int64) {
		t.Helper()
		var gotCompleted, gotPrompt int64
		if err := d.db.QueryRow(`SELECT last_turn_completed_at,last_user_prompt_at FROM sidebar_attention`).Scan(&gotCompleted, &gotPrompt); err != nil || gotCompleted != completed || gotPrompt != prompt {
			t.Fatalf("attention=%d/%d want=%d/%d: %v", gotCompleted, gotPrompt, completed, prompt, err)
		}
	}
	assert(120, 100)
	_, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "events", Cursor: "1"}, Batches: []StreamBatch{{Session: &b.Session, Messages: []Message{{ID: "u2", Role: "user", CreatedAt: 200, ConversationVisible: true, Position: 3}}}}})
	if err != nil {
		t.Fatal(err)
	}
	assert(120, 200)
	session, _, err := d.SessionState(t.Context(), b.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	b.Checkpoint = nil
	b.Complete = true
	b.ExpectedRevision = session.Revision
	b.Messages = nil
	commit(t, d, b)
	assert(0, 0)
	if err := d.Purge(t.Context(), b.Session.Key); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM sidebar_attention`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("purged attention retained: %d %v", n, err)
	}
}

func TestAttentionMigrationBackfillsWithoutChangingCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Messages[0].Role = "user"
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	removeIncrementalAnalyticsSchema(t, d)
	if _, err := d.db.Exec(`DROP TABLE sidebar_attention; PRAGMA user_version=12`); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var prompt int64
	if err := d.db.QueryRow(`SELECT last_user_prompt_at FROM sidebar_attention`).Scan(&prompt); err != nil || prompt != 11 {
		t.Fatalf("attention backfill=%d %v", prompt, err)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("checkpoint=%q %v", cursor, err)
	}
}
