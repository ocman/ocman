package state

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestConversationCacheMigrationPreservesApplicationState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureSchemaVersionTable(db); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for version := 1; version <= 124; version++ {
		if err := applyMigration(tx, version); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.Exec(`INSERT INTO schema_version VALUES(124,0);
		INSERT INTO projects_cache VALUES(1,'[{"directory":"/old-project"}]',1);
		INSERT INTO setting(key,value,updated_at) VALUES('user-preference','kept',0)`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	core, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer core.Close()
	if value, ok, err := core.GetSetting(t.Context(), "user-preference"); err != nil || !ok || value != "kept" {
		t.Fatalf("application setting lost: %q %v %v", value, ok, err)
	}
	var tables int
	if err := core.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='projects_cache'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatalf("conversation-derived cache retained in application database: %d %v", tables, err)
	}
}
