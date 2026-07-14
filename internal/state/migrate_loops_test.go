package state

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// seedV27 brings a fresh in-memory DB up through v27 (the schema right
// before the loop→workflow copy) so the migration test can insert
// representative pre-workflow loop rows and then run migrateToV28.
func seedV27(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := ensureSchemaVersionTable(db); err != nil {
		t.Fatalf("schema_version: %v", err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	for v := 1; v <= 27; v++ {
		if err := applyMigration(tx, v); err != nil {
			t.Fatalf("migrate v%d: %v", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, v); err != nil {
			t.Fatalf("record v%d: %v", v, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit seed: %v", err)
	}
	return db
}

// insertLoopRow writes a minimal loops row directly (bypassing the domain
// layer) so the test drives the raw DB shape a pre-workflow ocman produced.
func insertLoopRow(t *testing.T, db *sql.DB, id, triggerType, triggerConfig, actionType, state string, iteration int) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO loops
			(id, platform, root_session_id, directory, trigger_type,
			 trigger_config, action_type, action_template, stop_conditions,
			 state, iteration, created_at, updated_at, session_mode)
		VALUES (?, 'opencode', 'root-sess', '/repo', ?, ?, ?, 'do work',
			'{"max_iterations":25,"max_cost_usd":5}', ?, ?, 100, 200, 'fresh')
	`, id, triggerType, triggerConfig, actionType, state, iteration)
	if err != nil {
		t.Fatalf("insert loop %s: %v", id, err)
	}
}

func insertLoopIterationRow(t *testing.T, db *sql.DB, loopID string, seq int, outcome, childSession string) {
	t.Helper()
	_, err := db.Exec(`
		INSERT INTO loop_iterations
			(loop_id, seq, fired_at, started_at, completed_at, trigger_detail,
			 rendered_prompt, child_session_id, outcome, summary)
		VALUES (?, ?, ?, ?, ?, 'because', 'do work', ?, ?, 'summary text')
	`, loopID, seq, 1000+seq, 1000+seq, 2000+seq, childSession, outcome)
	if err != nil {
		t.Fatalf("insert iteration %s#%d: %v", loopID, seq, err)
	}
}

func TestMigrateV28_CopiesEachLoopToOneNodeWorkflow(t *testing.T) {
	db := seedV27(t)
	// One loop per pre-workflow lifecycle state, covering every trigger type.
	insertLoopRow(t, db, "loop_active", "schedule", `{"interval_seconds":300}`, "prompt_root", "active", 2)
	insertLoopRow(t, db, "loop_paused", "cron", `{"cron_expr":"0 * * * *"}`, "spawn_child", "paused", 1)
	insertLoopRow(t, db, "loop_done", "pr_event", `{"pr_number":42}`, "spawn_worktree", "completed", 3)
	insertLoopRow(t, db, "loop_err", "turn_complete", `{}`, "prompt_child", "errored", 1)
	insertLoopRow(t, db, "loop_deleted", "schedule", `{"interval_seconds":60}`, "prompt_root", "deleted", 0)

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV28(tx); err != nil {
		t.Fatalf("migrateToV28: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	// Every non-deleted loop becomes exactly one one-node workflow
	// definition + version, mapped for id resolution. Deleted loops are
	// skipped (terminal, hidden).
	wantWorkflows := map[string]struct {
		trigger string
	}{
		"loop_active": {"interval"},
		"loop_paused": {"cron"},
		"loop_done":   {"pr"},
		"loop_err":    {"turn_completion"},
	}
	for loopID, want := range wantWorkflows {
		var versionID, workflowID string
		if err := db.QueryRow(`SELECT version_id, workflow_id FROM loop_workflow_map WHERE loop_id = ?`, loopID).Scan(&versionID, &workflowID); err != nil {
			t.Fatalf("mapping missing for %s: %v", loopID, err)
		}
		var definition string
		var concurrency int
		if err := db.QueryRow(`SELECT definition_json, concurrency FROM workflow_version WHERE id = ?`, versionID).Scan(&definition, &concurrency); err != nil {
			t.Fatalf("version missing for %s: %v", loopID, err)
		}
		if concurrency != 1 {
			t.Errorf("%s: concurrency = %d, want 1", loopID, concurrency)
		}
		if !strings.Contains(definition, `"type":"`+want.trigger+`"`) {
			t.Errorf("%s: trigger %q not in definition: %s", loopID, want.trigger, definition)
		}
		// Exactly one node in the version.
		var nodeCount int
		if err := db.QueryRow(`SELECT count(*) FROM workflow_version_node WHERE version_id = ?`, versionID).Scan(&nodeCount); err != nil {
			t.Fatal(err)
		}
		if nodeCount != 1 {
			t.Errorf("%s: %d nodes, want 1 (one-node workflow)", loopID, nodeCount)
		}
		// The loop's compatibility policies survive round-trip.
		if !strings.Contains(definition, `"loopCompat"`) {
			t.Errorf("%s: loopCompat policies not preserved: %s", loopID, definition)
		}
		// The definition JSON decodes into the workflow-definition shape
		// the workflows domain consumes (one node, one trigger, concurrency
		// 1) — i.e. it's a real one-node workflow, not opaque bytes.
		var decoded struct {
			ID          string `json:"id"`
			Concurrency int    `json:"concurrency"`
			Triggers    []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"triggers"`
			Nodes []struct {
				ID   string `json:"id"`
				Type string `json:"type"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal([]byte(definition), &decoded); err != nil {
			t.Fatalf("%s: definition does not decode as workflow JSON: %v", loopID, err)
		}
		if decoded.Concurrency != 1 || len(decoded.Nodes) != 1 || len(decoded.Triggers) != 1 {
			t.Errorf("%s: not a well-formed one-node workflow: %+v", loopID, decoded)
		}
		if decoded.Triggers[0].Type != want.trigger {
			t.Errorf("%s: decoded trigger type = %q, want %q", loopID, decoded.Triggers[0].Type, want.trigger)
		}
		if decoded.Nodes[0].Type != "agent" {
			t.Errorf("%s: decoded node type = %q, want agent", loopID, decoded.Nodes[0].Type)
		}
	}

	// Deleted loop is not migrated.
	var deletedCount int
	if err := db.QueryRow(`SELECT count(*) FROM loop_workflow_map WHERE loop_id = 'loop_deleted'`).Scan(&deletedCount); err != nil {
		t.Fatal(err)
	}
	if deletedCount != 0 {
		t.Errorf("deleted loop should not be migrated, got %d mappings", deletedCount)
	}
}

func TestMigrateV28_MapsIterationsToRunsAndAttempts(t *testing.T) {
	db := seedV27(t)
	insertLoopRow(t, db, "loop_hist", "schedule", `{"interval_seconds":300}`, "spawn_child", "completed", 3)
	insertLoopIterationRow(t, db, "loop_hist", 1, "ok", "child-1")
	insertLoopIterationRow(t, db, "loop_hist", 2, "error", "")
	insertLoopIterationRow(t, db, "loop_hist", 3, "ok", "child-3")

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV28(tx); err != nil {
		t.Fatalf("migrateToV28: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var versionID, workflowID string
	if err := db.QueryRow(`SELECT version_id, workflow_id FROM loop_workflow_map WHERE loop_id = 'loop_hist'`).Scan(&versionID, &workflowID); err != nil {
		t.Fatalf("mapping missing: %v", err)
	}
	// One run per iteration.
	var runCount int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_run WHERE version_id = ?`, versionID).Scan(&runCount); err != nil {
		t.Fatal(err)
	}
	if runCount != 3 {
		t.Fatalf("run count = %d, want 3 (one per iteration)", runCount)
	}
	// Run states map from iteration outcomes.
	states := map[string]int{}
	rows, err := db.Query(`SELECT state FROM workflow_run WHERE version_id = ?`, versionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		states[s]++
	}
	if states["successful"] != 2 || states["failed"] != 1 {
		t.Errorf("run states = %v, want 2 successful + 1 failed", states)
	}
	// Each run has exactly one node attempt (faithful one-node history),
	// and the child session id is carried onto the attempt.
	var attemptCount, sessionAttempts int
	if err := db.QueryRow(`SELECT count(*) FROM workflow_node_attempt WHERE run_id IN (SELECT id FROM workflow_run WHERE version_id = ?)`, versionID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 3 {
		t.Errorf("attempt count = %d, want 3", attemptCount)
	}
	if err := db.QueryRow(`SELECT count(*) FROM workflow_node_attempt WHERE session_id != '' AND run_id IN (SELECT id FROM workflow_run WHERE version_id = ?)`, versionID).Scan(&sessionAttempts); err != nil {
		t.Fatal(err)
	}
	if sessionAttempts != 2 {
		t.Errorf("session-linked attempts = %d, want 2", sessionAttempts)
	}
}

func TestMigrateV28_AvoidsExistingWorkflowRunID(t *testing.T) {
	db := seedV27(t)
	insertLoopRow(t, db, "loop_collision", "schedule", `{"interval_seconds":300}`, "prompt_root", "completed", 1)
	insertLoopIterationRow(t, db, "loop_collision", 1, "ok", "")
	if _, err := db.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at) VALUES ('wfr_loop_loop_collision_1', 'existing', 'existing', 'successful', 1, 1)`); err != nil {
		t.Fatalf("insert colliding run: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var runID string
	if err := db.QueryRow(`SELECT id FROM workflow_run WHERE version_id = 'wfv_loop_loop_collision'`).Scan(&runID); err != nil {
		t.Fatalf("find migrated run: %v", err)
	}
	if runID == "wfr_loop_loop_collision_1" {
		t.Fatal("migration reused an existing workflow run ID")
	}
}

func TestMigrateV28_Idempotent(t *testing.T) {
	db := seedV27(t)
	insertLoopRow(t, db, "loop_x", "schedule", `{"interval_seconds":300}`, "prompt_root", "active", 1)
	insertLoopIterationRow(t, db, "loop_x", 1, "ok", "child-1")

	run := func() {
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := migrateToV28(tx); err != nil {
			t.Fatalf("migrateToV28: %v", err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	run()
	run() // second run must be a no-op

	var mappings, versions, runs int
	if err := db.QueryRow(`SELECT count(*) FROM loop_workflow_map`).Scan(&mappings); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM workflow_version`).Scan(&versions); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM workflow_run`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if mappings != 1 || versions != 1 || runs != 1 {
		t.Fatalf("re-run duplicated data: mappings=%d versions=%d runs=%d, want 1/1/1", mappings, versions, runs)
	}
}

// TestMigrateV28_FreshDB verifies a fresh database (no loops) migrates
// cleanly to the latest version and the copy is a no-op.
func TestMigrateV28_FreshDB(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != latestSchemaVersion {
		t.Errorf("fresh DB at v%d, want v%d", version, latestSchemaVersion)
	}
	var mappings int
	if err := db.QueryRow(`SELECT count(*) FROM loop_workflow_map`).Scan(&mappings); err != nil {
		t.Fatalf("loop_workflow_map not created on fresh DB: %v", err)
	}
	if mappings != 0 {
		t.Errorf("fresh DB has %d mappings, want 0", mappings)
	}
}

// TestMigrateV28_EndToEndOldDB runs the full migrate() over a DB seeded
// at v27 with loops, exercising the real startup path (not just the
// isolated step) to prove old databases upgrade end to end.
func TestMigrateV28_EndToEndOldDB(t *testing.T) {
	db := seedV27(t)
	insertLoopRow(t, db, "loop_e2e", "cron", `{"cron_expr":"*/5 * * * *"}`, "prompt_root", "active", 1)
	insertLoopIterationRow(t, db, "loop_e2e", 1, "ok", "child-1")

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var versionID string
	if err := db.QueryRow(`SELECT version_id FROM loop_workflow_map WHERE loop_id = 'loop_e2e'`).Scan(&versionID); err != nil {
		t.Fatalf("loop not migrated end to end: %v", err)
	}
	// Re-running migrate on an already-current DB is a no-op.
	if err := migrate(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
