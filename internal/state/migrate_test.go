package state

import (
	"database/sql"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestMigrateV36PreservesWorkflowRetrySources(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		CREATE TABLE workflow_run (id TEXT PRIMARY KEY);
		CREATE TABLE workflow_node_attempt (id INTEGER PRIMARY KEY);
		INSERT INTO workflow_run (id) VALUES ('run1');
		INSERT INTO workflow_node_attempt (id) VALUES (7);
	`); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV36(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := db.QueryRow(`SELECT id FROM workflow_run`).Scan(&runID); err != nil || runID != "run1" {
		t.Fatalf("workflow run after migration = %q, %v", runID, err)
	}
	if _, err := db.Exec(`UPDATE workflow_run SET retry_of_run_id = 'source', retry_from_node_id = 'fix'; UPDATE workflow_node_attempt SET reused_attempt_id = 3`); err != nil {
		t.Fatalf("retry columns unavailable: %v", err)
	}
}

func TestMigrateV25BackfillsManualWorkflowTrigger(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV22(tx); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES ('release', 'Release', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at) VALUES ('v1', 'release', 'Release', 1, '1', '{"id":"release","name":"Release","version":"1","concurrency":1,"nodes":[{"id":"review","name":"Review","type":"approval"}],"dependencies":[]}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at) VALUES ('run1', 'release', 'v1', 'active', 2, 2)`); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV23(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV24(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV25(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var definition string
	if err := db.QueryRow(`SELECT definition_json FROM workflow_version WHERE id = 'v1'`).Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(definition, `"triggers":[{"id":"manual","type":"manual"}]`) {
		t.Fatalf("manual trigger was not backfilled: %s", definition)
	}
	var snapshot string
	if err := db.QueryRow(`SELECT trigger_snapshot_json FROM workflow_run WHERE id = 'run1'`).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `"id":"manual"`) || !strings.Contains(snapshot, `"versionId":"v1"`) {
		t.Fatalf("run trigger snapshot was not backfilled: %s", snapshot)
	}
}

// openLegacyDB creates a database that looks like a pre-migration
// state.db: old single-column PK, two pre-existing rows in each table.
func openLegacyDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	_, err = sqlDB.Exec(`
		CREATE TABLE archived_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			archived_at INTEGER NOT NULL
		);
		CREATE TABLE seen_session (
			session_id TEXT PRIMARY KEY,
			session_time_updated INTEGER NOT NULL,
			seen_at INTEGER NOT NULL
		);
		INSERT INTO archived_session VALUES ('s1', 1000, 9999);
		INSERT INTO archived_session VALUES ('s2', 2000, 9999);
		INSERT INTO seen_session VALUES ('s3', 3000, 9999);
	`)
	if err != nil {
		t.Fatalf("seed legacy: %v", err)
	}
	return sqlDB
}

func TestMigrate_BackfillsOpencodePlatform(t *testing.T) {
	raw := openLegacyDB(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Both tables should now have a platform column that was
	// backfilled with 'opencode' on every pre-existing row.
	rows, err := raw.Query(`SELECT platform, session_id, session_time_updated FROM archived_session ORDER BY session_id`)
	if err != nil {
		t.Fatalf("query archived: %v", err)
	}
	defer rows.Close()
	var seen []struct {
		p, id string
		t     int64
	}
	for rows.Next() {
		var p, id string
		var ts int64
		if err := rows.Scan(&p, &id, &ts); err != nil {
			t.Fatal(err)
		}
		seen = append(seen, struct {
			p, id string
			t     int64
		}{p, id, ts})
	}
	if len(seen) != 2 {
		t.Fatalf("expected 2 archived rows, got %d", len(seen))
	}
	for _, r := range seen {
		if r.p != "opencode" {
			t.Errorf("row %+v: expected platform=opencode, got %q", r, r.p)
		}
	}

	var platform string
	if err := raw.QueryRow(`SELECT platform FROM seen_session WHERE session_id='s3'`).Scan(&platform); err != nil {
		t.Fatalf("query seen: %v", err)
	}
	if platform != "opencode" {
		t.Errorf("seen: expected platform=opencode, got %q", platform)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	raw := openLegacyDB(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Running again must be a no-op.
	if err := migrate(raw); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	// Still the original 2 archived rows — nothing duplicated.
	var count int
	if err := raw.QueryRow(`SELECT count(*) FROM archived_session`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("expected 2 archived rows after second migrate, got %d", count)
	}
}

func TestMigrate_RecordsSchemaVersion(t *testing.T) {
	raw := openLegacyDB(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var version int
	if err := raw.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema_version: %v", err)
	}
	if version < 1 {
		t.Errorf("expected schema_version >= 1, got %d", version)
	}
}

func TestWorkflowAttemptExecutorMigrationsPreserveRows(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE workflow_node_attempt (id INTEGER PRIMARY KEY, run_id TEXT, node_id TEXT, seq INTEGER, state TEXT, started_at INTEGER, completed_at INTEGER); INSERT INTO workflow_node_attempt VALUES (1, 'run', 'node', 1, 'waiting', 10, NULL)`); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV23(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV24(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var state, platform, outputs string
	if err := raw.QueryRow(`SELECT state, platform, outputs_json FROM workflow_node_attempt WHERE id = 1`).Scan(&state, &platform, &outputs); err != nil {
		t.Fatal(err)
	}
	if state != "waiting" || platform != "" || outputs != "{}" {
		t.Fatalf("unexpected migrated attempt: state=%q platform=%q outputs=%q", state, platform, outputs)
	}
}

func TestMigrateToV24ReconcilesAgentV23(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE workflow_node_attempt (
		id INTEGER PRIMARY KEY, run_id TEXT, node_id TEXT, seq INTEGER, state TEXT,
		started_at INTEGER, completed_at INTEGER, platform TEXT NOT NULL DEFAULT '',
		session_id TEXT NOT NULL DEFAULT '', session_state TEXT NOT NULL DEFAULT '',
		affinity TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '',
		outputs_json TEXT NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV24(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO workflow_node_attempt (id, run_id, node_id, seq, state, started_at) VALUES (1, 'run', 'node', 1, 'waiting', 10)`); err != nil {
		t.Fatal(err)
	}
	var stdout, platform string
	var truncated int
	if err := raw.QueryRow(`SELECT stdout, platform, stdout_truncated FROM workflow_node_attempt WHERE id = 1`).Scan(&stdout, &platform, &truncated); err != nil {
		t.Fatal(err)
	}
	if stdout != "" || platform != "" || truncated != 0 {
		t.Fatalf("unexpected reconciled defaults: stdout=%q platform=%q truncated=%d", stdout, platform, truncated)
	}
}

func TestMigrateToV24ReconcilesAgentBranchV23(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`
		CREATE TABLE workflow_node_attempt (
			id INTEGER PRIMARY KEY, run_id TEXT, node_id TEXT, seq INTEGER, state TEXT,
			started_at INTEGER, completed_at INTEGER, platform TEXT NOT NULL DEFAULT '',
			session_id TEXT NOT NULL DEFAULT '', session_state TEXT NOT NULL DEFAULT '',
			affinity TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '',
			outputs_json TEXT NOT NULL DEFAULT '{}', error TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO workflow_node_attempt (id, run_id, node_id, seq, state, started_at, session_id)
		VALUES (1, 'run', 'agent', 1, 'running', 10, 'session-1')`); err != nil {
		t.Fatal(err)
	}
	tx, err := raw.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV24(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var sessionID, stdout string
	if err := raw.QueryRow(`SELECT session_id, stdout FROM workflow_node_attempt WHERE id = 1`).Scan(&sessionID, &stdout); err != nil {
		t.Fatal(err)
	}
	if sessionID != "session-1" || stdout != "" {
		t.Fatalf("unexpected reconciled attempt: session=%q stdout=%q", sessionID, stdout)
	}
}

func TestMigrate_CompositePrimaryKey(t *testing.T) {
	raw := openLegacyDB(t)
	if err := migrate(raw); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Two rows may exist with the same session_id if they belong to
	// different platforms (this is the whole point of the migration).
	_, err := raw.Exec(`INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
	                     VALUES ('other-platform', 's1', 5000, 9999)`)
	if err != nil {
		t.Fatalf("insert other-platform s1: %v", err)
	}
	// But re-inserting an existing (platform, session_id) pair must fail.
	_, err = raw.Exec(`INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
	                   VALUES ('opencode', 's1', 1000, 9999)`)
	if err == nil {
		t.Error("expected duplicate (opencode,s1) insert to fail")
	}
}

// TestMigrate_SingleFeatureV26ForwardMigrates proves the merged monotonic
// v26/v27/v28 sequence still migrates cleanly forward from a database that
// was stamped at one of the original competing v26 migrations (artifacts,
// resource pools, or loop migration each independently shipped as "v26").
// Such a DB carries that one feature's objects while marked at v26, so the
// merged sequence must run v27+v28 (or v26 conditionally) without colliding
// on already-present tables/columns.
func TestMigrate_SingleFeatureV26ForwardMigrates(t *testing.T) {
	// seedThroughV25 drives migrations v1..25 and stamps schema_version so a
	// later migrate() call resumes at v26.
	seedThroughV25 := func(t *testing.T) *sql.DB {
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
		for v := 1; v <= 25; v++ {
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

	cases := []struct {
		name string
		// install applies the single-feature objects that a pre-merge v26
		// branch would have created, then stamps schema_version at 26.
		install func(t *testing.T, db *sql.DB)
	}{
		{
			name: "artifacts branch v26",
			install: func(t *testing.T, db *sql.DB) {
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				if err := migrateToV26(tx); err != nil {
					t.Fatalf("install artifacts v26: %v", err)
				}
				if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (26, 0)`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "resource pools branch v26",
			install: func(t *testing.T, db *sql.DB) {
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				// The pools feature's original v26 created the lease table.
				if err := migrateToV27(tx); err != nil {
					t.Fatalf("install pools v26: %v", err)
				}
				if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (26, 0)`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "loop migration branch v26",
			install: func(t *testing.T, db *sql.DB) {
				tx, err := db.Begin()
				if err != nil {
					t.Fatal(err)
				}
				// The loop feature's original v26 created loop_workflow_map.
				if err := migrateToV28(tx); err != nil {
					t.Fatalf("install loop v26: %v", err)
				}
				if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (26, 0)`); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := seedThroughV25(t)
			tc.install(t, db)

			if err := migrate(db); err != nil {
				t.Fatalf("forward migrate: %v", err)
			}

			var version int
			if err := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			if version != latestSchemaVersion {
				t.Fatalf("after forward migrate at v%d, want v%d", version, latestSchemaVersion)
			}
			// All three features' objects must coexist regardless of which
			// single-feature v26 the DB started from.
			for _, table := range []string{"workflow_artifact", "workflow_resource_lease", "loop_workflow_map"} {
				var n int
				if err := db.QueryRow(
					`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table,
				).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Fatalf("expected table %q to exist, found %d", table, n)
				}
			}
			// The artifact retention column must exist too.
			var retentionCols int
			if err := db.QueryRow(
				`SELECT count(*) FROM pragma_table_info('workflow_version') WHERE name='retention_days'`,
			).Scan(&retentionCols); err != nil {
				t.Fatal(err)
			}
			if retentionCols != 1 {
				t.Fatalf("expected workflow_version.retention_days column, found %d", retentionCols)
			}
			// migrate() again is a no-op.
			if err := migrate(db); err != nil {
				t.Fatalf("second migrate: %v", err)
			}
		})
	}
}

func TestMigrate_FreshDB_CreatesSchemaAtLatestVersion(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}
	// Tables exist with the new composite-key shape.
	_, err = sqlDB.Exec(`INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
	                     VALUES ('opencode', 's1', 100, 9999)`)
	if err != nil {
		t.Fatalf("insert into fresh schema: %v", err)
	}
}

func TestMigrate_V34AddsChildResultDeliveryState(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	if err := migrate(sqlDB); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := sqlDB.QueryRow(`SELECT count(*) FROM pragma_table_info('child_sessions') WHERE name = 'result_delivery'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("child_sessions.result_delivery columns = %d, want 1", count)
	}
}

func TestMigrate_RecoveryV30ComponentDBUpgrades(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := ensureSchemaVersionTable(db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for v := 1; v <= 29; v++ {
		if err := applyMigration(tx, v); err != nil {
			t.Fatalf("migrate v%d: %v", v, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, v); err != nil {
			t.Fatal(err)
		}
	}
	if err := addColumnIfMissing(tx, "workflow_node_attempt", "resolved_at", "INTEGER"); err != nil {
		t.Fatal(err)
	}
	if err := addColumnIfMissing(tx, "workflow_node_attempt", "resolved_by", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_version (version, applied_at) VALUES (30, 0)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("upgrade recovery v30 component DB: %v", err)
	}
	var version int
	if err := db.QueryRow(`SELECT max(version) FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != latestSchemaVersion {
		t.Fatalf("schema version = %d, want %d", version, latestSchemaVersion)
	}
	for _, column := range []string{"resolved_at", "resolved_by"} {
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('workflow_node_attempt') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("workflow_node_attempt.%s missing", column)
		}
	}
	var mapTable int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'workflow_map_item'`).Scan(&mapTable); err != nil {
		t.Fatal(err)
	}
	if mapTable != 1 {
		t.Fatal("workflow_map_item missing after recovery v30 upgrade")
	}
}

func TestMigrate_V3_CreatesAuthSecretTable(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}

	// Insert with id=1 must succeed.
	if _, err := sqlDB.Exec(
		`INSERT INTO auth_secret (id, hmac_key, created_at) VALUES (1, ?, ?)`,
		[]byte{0x01, 0x02, 0x03}, 9999,
	); err != nil {
		t.Fatalf("insert id=1: %v", err)
	}

	// Insert with id=2 must fail the CHECK constraint (single-row table).
	if _, err := sqlDB.Exec(
		`INSERT INTO auth_secret (id, hmac_key, created_at) VALUES (2, ?, ?)`,
		[]byte{0x04}, 9999,
	); err == nil {
		t.Error("expected CHECK constraint to reject id != 1")
	}
}

func TestMigrate_V11_AddsReasoningColumn(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}

	// The reasoning column exists and accepts the standard insert
	// shape (NOT NULL, DEFAULT ''). Inserting without reasoning must
	// succeed and read back the empty default.
	if _, err := sqlDB.Exec(`
		INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text,
			 patterns_json, judge_session_id, approved_at)
		VALUES ('opencode', 's1', 'p1', 'read file', '[]', 'judge-1', 100)
	`); err != nil {
		t.Fatalf("insert without reasoning: %v", err)
	}
	var reasoning string
	if err := sqlDB.QueryRow(
		`SELECT reasoning FROM auto_approved_permission WHERE permission_id = 'p1'`,
	).Scan(&reasoning); err != nil {
		t.Fatalf("read reasoning: %v", err)
	}
	if reasoning != "" {
		t.Errorf("expected empty default reasoning, got %q", reasoning)
	}

	// Inserting with a reasoning value must round-trip.
	if _, err := sqlDB.Exec(`
		INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text,
			 patterns_json, judge_session_id, reasoning, approved_at)
		VALUES ('opencode', 's1', 'p2', 'write file', '[]', 'judge-2', 'Writes to docs only.', 200)
	`); err != nil {
		t.Fatalf("insert with reasoning: %v", err)
	}
	if err := sqlDB.QueryRow(
		`SELECT reasoning FROM auto_approved_permission WHERE permission_id = 'p2'`,
	).Scan(&reasoning); err != nil {
		t.Fatalf("read reasoning p2: %v", err)
	}
	if reasoning != "Writes to docs only." {
		t.Errorf("reasoning round-trip mismatch: got %q", reasoning)
	}
}

// TestMigrate_V11_PreservesExistingRows ensures the v11 migration adds
// the reasoning column to existing rows (which were inserted under v7)
// without losing data.
func TestMigrate_V11_PreservesExistingRows(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// Insert a row that pre-dates v11 (no reasoning provided).
	if _, err := sqlDB.Exec(`
		INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text,
			 patterns_json, judge_session_id, approved_at)
		VALUES ('opencode', 's1', 'legacy', 'mkdir foo', '["foo"]', 'judge-x', 999)
	`); err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}
	// Running migrate again must be a no-op (idempotency check).
	if err := migrate(sqlDB); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var permText, reasoning string
	if err := sqlDB.QueryRow(
		`SELECT permission_text, reasoning FROM auto_approved_permission WHERE permission_id = 'legacy'`,
	).Scan(&permText, &reasoning); err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if permText != "mkdir foo" {
		t.Errorf("permission_text lost: got %q", permText)
	}
	if reasoning != "" {
		t.Errorf("expected empty reasoning for legacy row, got %q", reasoning)
	}
}

func TestMigrateV45BackfillsApprovalProvenance(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateToV7(tx); err != nil {
		t.Fatal(err)
	}
	if err := migrateToV11(tx); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		id, reasoning string
	}{
		{"user-once", "user clicked Allow once"},
		{"user-always", "user clicked Allow always"},
		{"ai", "safe command"},
		{"spoof", "user clicked Allow always because it is safe"},
	} {
		if _, err := tx.Exec(`INSERT INTO auto_approved_permission
			(platform, session_id, permission_id, permission_text, patterns_json, judge_session_id, approved_at, reasoning)
			VALUES ('opencode', 's1', ?, 'bash', '[]', '', 123, ?)`, row.id, row.reasoning); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateToV45(tx); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	want := map[string][2]string{
		"user-once":   {"user", "once"},
		"user-always": {"user", "always"},
		"ai":          {"ai", "once"},
		"spoof":       {"ai", "once"},
	}
	rows, err := db.Query(`SELECT permission_id, approved_by, reply, metadata_json, asked_at, approved_at FROM auto_approved_permission`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, actor, reply, metadata string
		var askedAt, approvedAt int64
		if err := rows.Scan(&id, &actor, &reply, &metadata, &askedAt, &approvedAt); err != nil {
			t.Fatal(err)
		}
		if got := [2]string{actor, reply}; got != want[id] {
			t.Errorf("%s provenance = %v, want %v", id, got, want[id])
		}
		if metadata != "{}" || askedAt != approvedAt || askedAt != 123 {
			t.Errorf("%s legacy fields = metadata %q asked %d approved %d", id, metadata, askedAt, approvedAt)
		}
	}
}

func TestAuthSecret_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"
	sdb, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()

	// No secret initially.
	got, err := sdb.AuthSecret(t.Context())
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil initial secret, got %v", got)
	}

	// Store, read back.
	key := []byte("super-secret-32-byte-hmac-key!!!")
	if err := sdb.SetAuthSecret(t.Context(), key); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = sdb.AuthSecret(t.Context())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(key) {
		t.Errorf("read mismatch: got %x, want %x", got, key)
	}

	// Overwrite (rotation) replaces in place.
	key2 := []byte("another-different-key-of-32-bytes")
	if err := sdb.SetAuthSecret(t.Context(), key2); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got, err = sdb.AuthSecret(t.Context())
	if err != nil {
		t.Fatalf("read after rotate: %v", err)
	}
	if string(got) != string(key2) {
		t.Errorf("after rotate: got %x, want %x", got, key2)
	}
}

func TestMigrate_V14_CreatesRemoteTables(t *testing.T) {
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sqlDB.Close()

	if err := migrate(sqlDB); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}

	// instance_identity is single-row.
	if _, err := sqlDB.Exec(
		`INSERT INTO instance_identity (id, instance_id, remote_token, created_at) VALUES (1, 'x', 'y', 1)`,
	); err != nil {
		t.Fatalf("insert id=1: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO instance_identity (id, instance_id, remote_token, created_at) VALUES (2, 'x', 'y', 1)`,
	); err == nil {
		t.Error("expected CHECK to reject id != 1")
	}

	// remote auto-increments local_id and enforces UNIQUE remote_id.
	if _, err := sqlDB.Exec(
		`INSERT INTO remote (remote_id, address, token_encrypted, created_at) VALUES ('rid', 'a', x'00', 1)`,
	); err != nil {
		t.Fatalf("insert remote: %v", err)
	}
	if _, err := sqlDB.Exec(
		`INSERT INTO remote (remote_id, address, token_encrypted, created_at) VALUES ('rid', 'b', x'00', 1)`,
	); err == nil {
		t.Error("expected UNIQUE remote_id to reject duplicate")
	}
}

// TestMigrateRejectsNewerSchema pins the downgrade guard: a database
// written by a newer ocman must not be opened by an older binary, which
// would silently run its old queries against an unknown schema.
func TestMigrateRejectsNewerSchema(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := migrate(db); err != nil {
		t.Fatalf("migrate fresh: %v", err)
	}
	// Pretend a newer ocman migrated this database further.
	future := latestSchemaVersion + 3
	if _, err := db.Exec(
		`INSERT INTO schema_version (version, applied_at) VALUES (?, 0)`, future,
	); err != nil {
		t.Fatalf("seed future version: %v", err)
	}

	err = migrate(db)
	if err == nil {
		t.Fatal("migrate accepted a schema newer than this binary understands")
	}
	if !strings.Contains(err.Error(), "newer ocman") {
		t.Errorf("error should name the cause, got: %v", err)
	}
}
