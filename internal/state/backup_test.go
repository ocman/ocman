package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func legacyBackupDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state's.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA wal_autocheckpoint=0;
		CREATE TABLE archived_session (session_id TEXT PRIMARY KEY, session_time_updated INTEGER NOT NULL, archived_at INTEGER NOT NULL);
		CREATE TABLE seen_session (session_id TEXT PRIMARY KEY, session_time_updated INTEGER NOT NULL, seen_at INTEGER NOT NULL);
		INSERT INTO archived_session VALUES ('saved-session', 1000, 2000)`); err != nil {
		t.Fatal(err)
	}
	return db, path
}

func TestMigrationBackupPreservesWALAndOldSchema(t *testing.T) {
	raw, path := legacyBackupDB(t)
	wal, err := os.Stat(path + "-wal")
	if err != nil || wal.Size() == 0 {
		t.Fatalf("expected uncheckpointed WAL: %v", err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if version, err := currentSchemaVersion(raw); err != nil || version != latestSchemaVersion {
		t.Fatalf("upgraded version = %d, %v", version, err)
	}
	backups, err := filepath.Glob(path + ".backup-v1-to-v*-*.db")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v; want one", backups, err)
	}
	info, err := os.Stat(backups[0])
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup permissions = %v, %v", info, err)
	}
	backup, err := sql.Open("sqlite", backups[0]+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if version, err := currentSchemaVersion(backup); err != nil || version != 1 {
		t.Fatalf("backup version = %d, %v; want 1", version, err)
	}
	var session string
	if err := backup.QueryRow(`SELECT session_id FROM archived_session`).Scan(&session); err != nil || session != "saved-session" {
		t.Fatalf("backup row = %q, %v", session, err)
	}
	if _, err := backup.Exec(`SELECT platform FROM archived_session`); err == nil {
		t.Fatal("backup contains the migrated schema")
	}
	var integrity string
	if err := backup.QueryRow(`PRAGMA integrity_check`).Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("backup integrity = %q, %v", integrity, err)
	}
	if err := migrate(raw); err != nil {
		t.Fatal(err)
	}
	after, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(after) != 1 {
		t.Fatalf("unchanged schema created another backup: %v, %v", after, err)
	}
}

func TestMigrationBackupFailureBlocksUpgrade(t *testing.T) {
	raw, path := legacyBackupDB(t)
	dir := filepath.Dir(path)
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(dir+"-moved", dir) })
	err := migrate(raw)
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("expected backup error, got %v", err)
	}
	if version, err := currentSchemaVersion(raw); err != nil || version != 1 {
		t.Fatalf("failed backup changed schema to %d: %v", version, err)
	}
}

func TestMigrationBackupSnapshotFailureRemovesPartialFile(t *testing.T) {
	raw, path := legacyBackupDB(t)
	// VACUUM cannot run inside a transaction on the same connection.
	if _, err := raw.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = raw.Exec(`ROLLBACK`) }()
	if err := backupBeforeMigration(raw, 1); err == nil || !strings.Contains(err.Error(), "snapshotting") {
		t.Fatalf("expected snapshot failure, got %v", err)
	}
	files, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(files) != 0 {
		t.Fatalf("failed snapshot left files: %v, %v", files, err)
	}
}

func TestMigrationBackupRejectsClosedDatabase(t *testing.T) {
	raw, _ := legacyBackupDB(t)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := backupBeforeMigration(raw, 1); err == nil || !strings.Contains(err.Error(), "finding database path") {
		t.Fatalf("expected path lookup failure, got %v", err)
	}
}

func TestMigrationBackupRetainedAfterFailedMigration(t *testing.T) {
	raw, path := legacyBackupDB(t)
	// A view blocks v2's ALTER TABLE, after the backup must have completed.
	if _, err := raw.Exec(`DROP TABLE seen_session; CREATE VIEW seen_session AS SELECT 1`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrate(raw); err == nil {
			t.Fatal("expected migration failure")
		}
	}
	backups, err := filepath.Glob(path + ".backup-*.db")
	if err != nil || len(backups) != 2 {
		t.Fatalf("retries must retain distinct backups: %v, %v", backups, err)
	}
	if version, err := currentSchemaVersion(raw); err != nil || version != 1 {
		t.Fatalf("failed migration changed schema to %d: %v", version, err)
	}
}

func TestMigrationBackupSkippedWithoutUpgrade(t *testing.T) {
	for _, version := range []int{0, latestSchemaVersion, latestSchemaVersion + 1} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if err := ensureSchemaVersionTable(db); err != nil {
				t.Fatal(err)
			}
			if version > 0 {
				if _, err := db.Exec(`INSERT INTO schema_version VALUES (?, 0)`, version); err != nil {
					t.Fatal(err)
				}
			}
			err = migrate(db)
			if (err != nil) != (version > latestSchemaVersion) {
				t.Fatalf("migrate v%d: %v", version, err)
			}
			backups, err := filepath.Glob(path + ".backup-*")
			if err != nil || len(backups) != 0 {
				t.Fatalf("unexpected backups: %v, %v", backups, err)
			}
		})
	}
}
