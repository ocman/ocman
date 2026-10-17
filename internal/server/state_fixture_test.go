package server

import (
	"path/filepath"
	"testing"
)

func TestOpenTestStateDBAlreadyMigrated(t *testing.T) {
	first := openTestStateDB(t)
	for _, schema := range ModuleSchemas() {
		var version int
		if err := first.SQL().QueryRow(`SELECT max(version) FROM module_schema_version WHERE module = ?`, schema.Module).Scan(&version); err != nil {
			t.Fatal(err)
		}
		if version != len(schema.Migrations) {
			t.Fatalf("%s schema = %d, want %d", schema.Module, version, len(schema.Migrations))
		}
	}
	var path string
	if err := first.SQL().QueryRow(`SELECT file FROM pragma_database_list WHERE name = 'main'`).Scan(&path); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".backup-*")
	if err != nil || len(backups) != 0 {
		t.Fatalf("opening a migrated test fixture must not run migrations or create backups: %v, %v", backups, err)
	}
	if _, err := first.SQL().Exec(`INSERT INTO setting (key, value, updated_at) VALUES ('fixture-isolation', 'first', 1)`); err != nil {
		t.Fatal(err)
	}
	second := openTestStateDB(t)
	var count int
	if err := second.SQL().QueryRow(`SELECT count(*) FROM setting WHERE key = 'fixture-isolation'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("state leaked between test fixtures")
	}
}

func BenchmarkOpenTestStateDB(b *testing.B) {
	for b.Loop() {
		if err := openTestStateDB(b).Close(); err != nil {
			b.Fatal(err)
		}
	}
}
