package statetest

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
)

func testSchema(migrations int) state.ModuleSchema {
	schema := state.ModuleSchema{Module: "statetest", Tables: []string{"statetest_item"}}
	schema.Migrations = append(schema.Migrations, func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE statetest_item (id TEXT PRIMARY KEY)`)
		return err
	})
	for range migrations - 1 {
		schema.Migrations = append(schema.Migrations, func(*sql.Tx) error { return nil })
	}
	return schema
}

func TestPathWithModulesIsMigratedWithoutBackupAndIsolated(t *testing.T) {
	schema := testSchema(2)
	path := Path(t, schema)
	db, err := state.Open(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.SQL().QueryRow(`SELECT max(version) FROM module_schema_version WHERE module = ?`, schema.Module).Scan(&version); err != nil || version != 2 {
		t.Fatalf("module version = %d, %v; want 2", version, err)
	}
	if backups, err := filepath.Glob(path + ".backup-*"); err != nil || len(backups) != 0 {
		t.Fatalf("opening a migrated copy must not migrate or back up: %v, %v", backups, err)
	}
	if _, err := db.SQL().Exec(`INSERT INTO statetest_item (id) VALUES ('first')`); err != nil {
		t.Fatal(err)
	}

	other, err := state.Open(Path(t, schema), schema)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	var count int
	if err := other.SQL().QueryRow(`SELECT count(*) FROM statetest_item WHERE id = 'first'`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("state leaked between copies: %d, %v", count, err)
	}
}

func TestTemplateNameDistinguishesSchemaSets(t *testing.T) {
	names := map[string]bool{}
	for _, schemas := range [][]state.ModuleSchema{
		nil,
		{testSchema(1)},
		{testSchema(2)},
		{testSchema(1), {Module: "other"}},
		{{Module: "other"}, testSchema(1)},
	} {
		name := templateName(schemas)
		if names[name] {
			t.Fatalf("duplicate template name %q", name)
		}
		names[name] = true
	}
	if got := templateName(nil); got != "state.db" {
		t.Fatalf("core template name = %q", got)
	}
}
