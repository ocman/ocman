package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSessionInterruptionSurvivesReopenAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	database, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if database != nil {
			database.Close()
		}
	})
	first := SessionInterruption{MessageID: "m1", ObservedAt: 100, Message: "server restarted"}
	for _, row := range []struct {
		platform, session string
		notice            SessionInterruption
	}{
		{"opencode", "s1", first},
		{"opencode", "s1", SessionInterruption{MessageID: "m1", ObservedAt: 200, Message: "later observation"}},
		{"opencode", "s2", first},
		{"another-owner", "s1", first},
	} {
		if err := database.RecordSessionInterruption(t.Context(), row.platform, row.session, row.notice); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := database.SessionInterruptions(t.Context(), "opencode", "s1")
	if err != nil || len(got) != 1 || got[0] != first {
		t.Fatalf("persisted notices=%v err=%v", got, err)
	}
	ctx := t.Context()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.RecordSessionInterruption(ctx, "opencode", "s1", first); err == nil {
		t.Fatal("closed database accepted notice")
	}
	if _, err := database.SessionInterruptions(ctx, "opencode", "s1"); err == nil {
		t.Fatal("closed database read succeeded")
	}
}

func TestMigrateV114PreservesInterruptionHistory(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL); INSERT INTO schema_version VALUES (113, 0);
		CREATE TABLE seen_session (session_id TEXT PRIMARY KEY, interrupted INTEGER NOT NULL DEFAULT 0);
		INSERT INTO seen_session VALUES ('existing', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	var interrupted bool
	if err := database.QueryRow(`SELECT interrupted FROM seen_session WHERE session_id='existing'`).Scan(&interrupted); err != nil || !interrupted {
		t.Fatalf("migration lost existing seen-session state: %t %v", interrupted, err)
	}
	db := &DB{db: database}
	notice := SessionInterruption{MessageID: "m", ObservedAt: 1, Message: "interrupted"}
	if err := db.RecordSessionInterruption(t.Context(), "opencode", "s", notice); err != nil {
		t.Fatal(err)
	}
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	got, err := db.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0] != notice {
		t.Fatalf("migration lost notices: %v %v", got, err)
	}
}

func TestInterruptionBatchRollsBackWhenSecondWriteFails(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	if _, err := database.db.Exec(`CREATE TRIGGER reject_second_interruption BEFORE INSERT ON session_interruption
		WHEN (SELECT COUNT(*) FROM session_interruption) > 0 BEGIN SELECT RAISE(ABORT, 'second write failed'); END`); err != nil {
		t.Fatal(err)
	}
	err := database.RecordSessionInterruptions(t.Context(), "opencode", map[string]SessionInterruption{
		"first":  {MessageID: "m1", ObservedAt: 1, Message: "interrupted"},
		"second": {MessageID: "m2", ObservedAt: 1, Message: "interrupted"},
	})
	if err == nil {
		t.Fatal("failed batch reported success")
	}
	for _, id := range []string{"first", "second"} {
		got, err := database.SessionInterruptions(t.Context(), "opencode", id)
		if err != nil || len(got) != 0 {
			t.Fatalf("batch left partial notices: %s %v %v", id, got, err)
		}
	}
}

func TestPreparedInterruptionIsHiddenUntilStopConfirmation(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	notice := SessionInterruption{MessageID: "m", ObservedAt: 1, Message: "server stopped"}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": notice}); err != nil {
		t.Fatal(err)
	}
	if got, err := database.SessionInterruptions(t.Context(), "opencode", "s"); err != nil || len(got) != 0 {
		t.Fatalf("prepared history visible: %v %v", got, err)
	}
	ids, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/other", nil)
	if err != nil || len(ids) != 0 {
		t.Fatal("confirmation crossed server roots")
	}
	confirmed := notice
	confirmed.ObservedAt = 3
	ids, err = database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": confirmed})
	if err != nil || len(ids) != 1 || ids[0] != "s" {
		t.Fatalf("confirmation=%v %v", ids, err)
	}
	got, err := database.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0].ObservedAt != 3 {
		t.Fatalf("confirmed history=%v %v", got, err)
	}
	ids, err = database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", nil)
	if err != nil || len(ids) != 0 {
		t.Fatal("confirmation not idempotent")
	}
}

func TestObservedInterruptionCanConfirmAnAbandonedPreparation(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	prepared := SessionInterruption{MessageID: "m", ObservedAt: 1, Message: "replacement"}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": prepared}); err != nil {
		t.Fatal(err)
	}
	observed := SessionInterruption{MessageID: "m", ObservedAt: 2, Message: "live connection lost"}
	if err := database.RecordSessionInterruption(t.Context(), "opencode", "s", observed); err != nil {
		t.Fatal(err)
	}
	got, err := database.SessionInterruptions(t.Context(), "opencode", "s")
	if err != nil || len(got) != 1 || got[0] != observed {
		t.Fatalf("observed confirmation=%v %v", got, err)
	}
}

func TestRecoveryPreparationPreservesPendingEvidence(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	first := SessionInterruption{MessageID: "m1", ObservedAt: 1, Message: "requested restart"}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": first}); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", map[string]SessionInterruption{"s": {MessageID: "m1", ObservedAt: 2, Message: "retry"}}); err != nil {
		t.Fatal(err)
	}
	pending, err := database.PreparedSessionInterruptions(t.Context(), "opencode", "/root")
	if err != nil || len(pending) != 1 || pending["s"] != first {
		t.Fatalf("recovery erased original evidence: %v %v", pending, err)
	}
}

func TestFailedReplacementPreparationRefreshesCompleteBaseline(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	first := map[string]SessionInterruption{"s": {MessageID: "old", BaselineStatus: "busy"}, "deleted": {MessageID: "gone"}}
	second := map[string]SessionInterruption{"s": {MessageID: "followup", BaselineStatus: "waiting"}, "empty": {BaselineStatus: "done"}}
	for _, baseline := range []map[string]SessionInterruption{first, second} {
		if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
			t.Fatal(err)
		}
	}
	got, err := database.PreparedSessionInterruptions(t.Context(), "opencode", "/root")
	if err != nil || len(got) != 2 || got["s"] != second["s"] || got["empty"] != second["empty"] {
		t.Fatalf("stale baseline: %v %v", got, err)
	}
	var attempt int
	if err := database.db.QueryRow(`SELECT attempt FROM session_replacement`).Scan(&attempt); err != nil || attempt != 2 {
		t.Fatalf("attempt=%d err=%v", attempt, err)
	}
}

func TestFailedConfirmationPreservesStoppedAttempt(t *testing.T) {
	database := openTestStateDB(t)
	defer database.Close()
	baseline := map[string]SessionInterruption{"s": {MessageID: "m", BaselineStatus: "busy"}}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
		t.Fatal(err)
	}
	if err := database.MarkReplacementStopped(t.Context(), "opencode", "/root"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.Exec(`CREATE TRIGGER fail_confirmation BEFORE INSERT ON session_interruption BEGIN SELECT RAISE(ABORT, 'confirmation failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", baseline); err == nil {
		t.Fatal("confirmation succeeded")
	}
	stopped, err := database.ReplacementStopped(t.Context(), "opencode", "/root")
	if err != nil || !stopped {
		t.Fatalf("lost stopped phase: %t %v", stopped, err)
	}
	if err := database.PrepareSessionInterruptions(t.Context(), "opencode", "/root", nil); err != nil {
		t.Fatal(err)
	}
	got, err := database.PreparedSessionInterruptions(t.Context(), "opencode", "/root")
	if err != nil || got["s"] != baseline["s"] {
		t.Fatalf("lost stopped baseline: %v %v", got, err)
	}
	if _, err := database.db.Exec(`DROP TRIGGER fail_confirmation`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ConfirmSessionInterruptions(t.Context(), "opencode", "/root", baseline); err != nil {
		t.Fatal(err)
	}
	stopped, err = database.ReplacementStopped(t.Context(), "opencode", "/root")
	if err != nil || stopped {
		t.Fatalf("confirmation did not settle operation: %t %v", stopped, err)
	}
}

func TestV116DropsUnattributablePendingEvidenceOnly(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	_, err = database.Exec(`CREATE TABLE schema_version (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_version VALUES (115,0);
		CREATE TABLE session_interruption (platform TEXT, session_id TEXT, message_id TEXT, observed_at INTEGER, message TEXT, replacement_root TEXT, confirmed INTEGER);
		INSERT INTO session_interruption VALUES ('opencode','s','pending',1,'pending','/root',0), ('opencode','s','confirmed',2,'confirmed','/root',1)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate(database); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM session_interruption WHERE message_id='confirmed'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("confirmed history lost: %d %v", count, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM session_interruption WHERE confirmed=0`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unsafe evidence preserved: %d %v", count, err)
	}
}
