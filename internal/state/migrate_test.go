package state

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

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

func TestAuthSecret_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	dbPath := dir + "/state.db"
	sdb, err := Open(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer sdb.Close()

	// No secret initially.
	got, err := sdb.AuthSecret()
	if err != nil {
		t.Fatalf("initial read: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil initial secret, got %v", got)
	}

	// Store, read back.
	key := []byte("super-secret-32-byte-hmac-key!!!")
	if err := sdb.SetAuthSecret(key); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err = sdb.AuthSecret()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(key) {
		t.Errorf("read mismatch: got %x, want %x", got, key)
	}

	// Overwrite (rotation) replaces in place.
	key2 := []byte("another-different-key-of-32-bytes")
	if err := sdb.SetAuthSecret(key2); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	got, err = sdb.AuthSecret()
	if err != nil {
		t.Fatalf("read after rotate: %v", err)
	}
	if string(got) != string(key2) {
		t.Errorf("after rotate: got %x, want %x", got, key2)
	}
}
