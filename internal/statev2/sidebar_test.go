package statev2

import (
	"errors"
	"path/filepath"
	"testing"
)

func removeSidebarSchema(t *testing.T, d *DB) {
	t.Helper()
	removePreparationSchema(t, d)
	if _, err := d.db.Exec(`DROP TRIGGER sidebar_source_deleted; DROP TABLE session_sidebar; DROP TABLE sidebar_branch; DROP TABLE sidebar_observation; DROP TABLE sidebar_connection; DROP INDEX message_unread_range`); err != nil {
		t.Fatal(err)
	}
}

func TestSidebarProjectionTracksCorrectionsSnapshotsAndPurge(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Messages[0].ConversationVisible = true
	b.Messages[0].Finish = "stop"
	commit(t, d, b)
	assert := func(title string, at int64, finish string) {
		t.Helper()
		var gotTitle, gotFinish string
		var gotAt int64
		if err := d.db.QueryRow(`SELECT title,last_message_at,last_finish FROM session_sidebar WHERE instance_id='first' AND id='session'`).Scan(&gotTitle, &gotAt, &gotFinish); err != nil {
			t.Fatal(err)
		}
		if gotTitle != title || gotAt != at || gotFinish != finish {
			t.Fatalf("projection=%q %d %q", gotTitle, gotAt, gotFinish)
		}
	}
	assert("retained title", 11, "stop")
	b.Checkpoint = nil
	b.ExpectedRevision = 1
	b.Session.Title = "edited"
	b.Messages[0].CreatedAt = 30
	b.Messages[0].Finish = "error"
	commit(t, d, b)
	assert("edited", 30, "error")
	b.ExpectedRevision = 2
	b.Complete = true
	b.Messages = nil
	b.Parts = nil
	commit(t, d, b)
	assert("edited", 0, "")
	b.ExpectedRevision = 1
	if _, err := d.Commit(t.Context(), b); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	assert("edited", 0, "")
}

func TestSidebarProjectionTracksStreamUpdates(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	s := b.Session
	s.Title = "streamed title"
	s.UpdatedAt = 50
	_, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: "first", Checkpoint: CheckpointUpdate{Stream: "events", Cursor: "1"}, Batches: []StreamBatch{{Session: &s, Messages: []Message{{ID: "next", Role: "assistant", CreatedAt: 50, ConversationVisible: true, Finish: "stop"}}}}})
	if err != nil {
		t.Fatal(err)
	}
	var title string
	var at int64
	if err := d.db.QueryRow(`SELECT title,last_message_at FROM session_sidebar`).Scan(&title, &at); err != nil {
		t.Fatal(err)
	}
	if title != "streamed title" || at != 50 {
		t.Fatalf("stream did not maintain sidebar projection: %q %d", title, at)
	}
}

func TestSidebarMigrationBackfillsWithoutChangingCheckpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	removeSidebarSchema(t, d)
	removeIncrementalAnalyticsSchema(t, d)
	if _, err := d.db.Exec(`PRAGMA user_version=10`); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var at int64
	if err := d.db.QueryRow(`SELECT last_message_at FROM session_sidebar`).Scan(&at); err != nil || at != 11 {
		t.Fatalf("backfill=%d %v", at, err)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("checkpoint=%q %v", cursor, err)
	}
}

func TestSidebarRepairPreservesStreamProgress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Messages[0].ConversationVisible = true
	commit(t, d, b)
	removeIncrementalAnalyticsSchema(t, d)
	if _, err := d.db.Exec(`UPDATE session_sidebar SET title='stale'; PRAGMA user_version=11`); err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var title string
	if err := d.db.QueryRow(`SELECT title FROM session_sidebar`).Scan(&title); err != nil || title != "retained title" {
		t.Fatalf("repair=%q %v", title, err)
	}
	if cursor, err := d.Checkpoint(t.Context(), "first", "runtime/generation"); err != nil || cursor != "1" {
		t.Fatalf("repair changed cursor: %q %v", cursor, err)
	}
}

func TestSidebarObservationGenerationAndBranchOrdering(t *testing.T) {
	d := openTestDB(t)
	scope := Instance{OwnerID: "owner", Platform: "opencode", SourceKey: "first"}
	if err := d.BeginSidebarGeneration(t.Context(), Instance{}, "g"); err == nil {
		t.Fatal("missing scope accepted")
	}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "old"); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "old", 100, map[string]SidebarObservation{"session": {Status: "busy"}}); err != nil {
		t.Fatal(err)
	}
	if err := d.BeginSidebarGeneration(t.Context(), scope, "new"); err != nil {
		t.Fatal(err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "old", 1000, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("old runtime revived its lease: %v", err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "new", 200, map[string]SidebarObservation{"session": {Status: "waiting", PendingQuestion: true}}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := d.db.QueryRow(`SELECT status FROM sidebar_observation`).Scan(&status); err != nil || status != "waiting" {
		t.Fatalf("observation=%q %v", status, err)
	}
	if err := d.ReplaceSidebarObservations(t.Context(), scope, "new", 300, nil); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = d.db.QueryRow(`SELECT count(*) FROM sidebar_observation`).Scan(&n)
	if n != 0 {
		t.Fatal("disconnected observation survived")
	}
	for _, item := range []struct {
		branch string
		at     int64
	}{{"new", 20}, {"stale", 10}, {"", 30}} {
		if err := d.SetSidebarBranch(t.Context(), "owner", "/repo", item.branch, item.at); err != nil {
			t.Fatal(err)
		}
	}
	var branch string
	if err := d.db.QueryRow(`SELECT branch FROM sidebar_branch`).Scan(&branch); err != nil || branch != "" {
		t.Fatalf("branch=%q %v", branch, err)
	}
	if err := d.SetSidebarBranch(t.Context(), "", "/repo", "x", 1); err == nil {
		t.Fatal("unscoped branch accepted")
	}
}
