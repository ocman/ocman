package state

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

// openLegacyDB creates a database that looks like a pre-migration
// state.db: old single-column PK, two pre-existing rows in each table.
func openLegacyDB(t *testing.T) *sql.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite3", ":memory:")
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
	                     VALUES ('claude-code', 's1', 5000, 9999)`)
	if err != nil {
		t.Fatalf("insert claude-code s1: %v", err)
	}
	// But re-inserting an existing (platform, session_id) pair must fail.
	_, err = raw.Exec(`INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
	                   VALUES ('opencode', 's1', 1000, 9999)`)
	if err == nil {
		t.Error("expected duplicate (opencode,s1) insert to fail")
	}
}

func TestMigrate_FreshDB_CreatesSchemaAtLatestVersion(t *testing.T) {
	sqlDB, err := sql.Open("sqlite3", ":memory:")
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
