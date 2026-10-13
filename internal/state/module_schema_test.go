package state

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTable(name, columns string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := tx.Exec(fmt.Sprintf(`CREATE TABLE %s (%s)`, name, columns))
		return err
	}
}

func notesSchema(migrations ...func(*sql.Tx) error) ModuleSchema {
	if len(migrations) == 0 {
		migrations = []func(*sql.Tx) error{
			createTable("notes_item", `id TEXT PRIMARY KEY, body TEXT NOT NULL`),
			func(tx *sql.Tx) error {
				_, err := tx.Exec(`ALTER TABLE notes_item ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0`)
				return err
			},
		}
	}
	return ModuleSchema{Module: "notes", Tables: []string{"notes_item"}, Migrations: migrations}
}

func moduleVersions(t *testing.T, db *sql.DB, module string) []int {
	t.Helper()
	rows, err := db.Query(`SELECT version FROM module_schema_version WHERE module = ? ORDER BY version`, module)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

func backups(t *testing.T, path string) []string {
	t.Helper()
	files, err := filepath.Glob(path + ".backup-*.db")
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func openState(t *testing.T, path string, schemas ...ModuleSchema) *DB {
	t.Helper()
	db, err := Open(path, schemas...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestModuleSchemaFreshDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := openState(t, path, notesSchema())
	if got := moduleVersions(t, db.SQL(), "notes"); fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("versions = %v", got)
	}
	if _, err := db.SQL().Exec(`INSERT INTO notes_item (id, body, pinned) VALUES ('a', 'b', 1)`); err != nil {
		t.Fatal(err)
	}
	if files := backups(t, path); len(files) != 0 {
		t.Fatalf("fresh database backed up: %v", files)
	}
}

func TestModuleSchemaUpgradesLegacyDatabaseUnderOneBackup(t *testing.T) {
	raw, path := legacyBackupDB(t)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	db := openState(t, path, notesSchema())
	var platform string
	if err := db.SQL().QueryRow(`SELECT platform FROM archived_session WHERE session_id = 'saved-session'`).Scan(&platform); err != nil {
		t.Fatalf("legacy row lost: %v", err)
	}
	if got := moduleVersions(t, db.SQL(), "notes"); len(got) != 2 {
		t.Fatalf("versions = %v", got)
	}
	// The core backup predates both batches; a second snapshot would only
	// repeat the upgraded core schema.
	if files := backups(t, path); len(files) != 1 || !strings.Contains(files[0], ".backup-v1-to-v") {
		t.Fatalf("backups = %v", files)
	}
}

func TestModuleSchemaUpgradeAppliesOnlyNewVersions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := openState(t, path)
	if _, err := db.SQL().Exec(`INSERT INTO setting (key, value, updated_at) VALUES ('kept', 'yes', 0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	runs := 0
	first := func(tx *sql.Tx) error { runs++; return createTable("notes_item", `id TEXT PRIMARY KEY`)(tx) }
	openState(t, path, notesSchema(first)).Close()
	files := backups(t, path)
	if len(files) != 1 || !strings.Contains(files[0], fmt.Sprintf(".backup-v%d-modules-", latestSchemaVersion)) {
		t.Fatalf("existing database must be backed up before its first module migration: %v", files)
	}

	// Rerun: nothing pending, no backup, no migration.
	openState(t, path, notesSchema(first)).Close()
	if runs != 1 || len(backups(t, path)) != 1 {
		t.Fatalf("rerun: runs=%d backups=%v", runs, backups(t, path))
	}

	second := func(tx *sql.Tx) error {
		_, err := tx.Exec(`ALTER TABLE notes_item ADD COLUMN body TEXT`)
		return err
	}
	db = openState(t, path, notesSchema(first, second))
	if runs != 1 {
		t.Fatalf("v1 reran: %d", runs)
	}
	if got := moduleVersions(t, db.SQL(), "notes"); fmt.Sprint(got) != "[1 2]" {
		t.Fatalf("versions = %v", got)
	}
	var kept string
	if err := db.SQL().QueryRow(`SELECT value FROM setting WHERE key = 'kept'`).Scan(&kept); err != nil || kept != "yes" {
		t.Fatalf("core data lost: %q, %v", kept, err)
	}
	if len(backups(t, path)) != 2 {
		t.Fatalf("second upgrade not backed up: %v", backups(t, path))
	}
}

func TestModuleSchemaFailureRollsBackWholeBatch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	openState(t, path).Close()
	boom := errors.New("boom")
	other := ModuleSchema{Module: "other", Tables: []string{"other_item"},
		Migrations: []func(*sql.Tx) error{createTable("other_item", `id TEXT`)}}
	failing := notesSchema(createTable("notes_item", `id TEXT`), func(*sql.Tx) error { return boom })

	for range 2 { // a retry fails the same way and advances nothing
		if _, err := Open(path, other, failing); !errors.Is(err, boom) || !strings.Contains(err.Error(), "notes to v2") {
			t.Fatalf("err = %v", err)
		}
	}
	db := openState(t, path)
	if tableExists(t, db.SQL(), "notes_item") || tableExists(t, db.SQL(), "other_item") {
		t.Fatal("failed batch left tables behind")
	}
	if got := moduleVersions(t, db.SQL(), "notes"); len(got) != 0 {
		t.Fatalf("failed batch advanced notes to %v", got)
	}
	if got := moduleVersions(t, db.SQL(), "other"); len(got) != 0 {
		t.Fatalf("failed batch advanced other to %v", got)
	}
	if n := len(backups(t, path)); n != 2 {
		t.Fatalf("each attempt keeps its backup, got %d", n)
	}
}

func TestModuleSchemaNewerVersionIsLeftAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := openState(t, path, notesSchema())
	if _, err := db.SQL().Exec(`INSERT INTO module_schema_version VALUES ('notes', 3, 0)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	other := ModuleSchema{Module: "other", Tables: []string{"other_item"},
		Migrations: []func(*sql.Tx) error{createTable("other_item", `id TEXT`)}}
	db = openState(t, path, notesSchema(), other)
	if got := moduleVersions(t, db.SQL(), "notes"); fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("newer module touched: %v", got)
	}
	if !tableExists(t, db.SQL(), "other_item") {
		t.Fatal("a newer module must not block the others")
	}
}

func TestModuleSchemaSkippedForNewerCore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	db := openState(t, path)
	if _, err := db.SQL().Exec(`INSERT INTO schema_version VALUES (?, 0)`, latestSchemaVersion+1); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	db = openState(t, path, notesSchema())
	if tableExists(t, db.SQL(), "notes_item") || tableExists(t, db.SQL(), "module_schema_version") {
		t.Fatal("module migrations ran against a newer core schema")
	}
}

func TestModuleSchemaValidationFailsClosed(t *testing.T) {
	ok := func(*sql.Tx) error { return nil }
	cases := map[string][]ModuleSchema{
		"without a module name": {{Migrations: []func(*sql.Tx) error{ok}}},
		"registered twice":      {notesSchema(), notesSchema()},
		"is nil":                {{Module: "notes", Migrations: []func(*sql.Tx) error{nil}}},
		"claimed by modules": {notesSchema(),
			{Module: "other", Tables: []string{"notes_item"}, Migrations: []func(*sql.Tx) error{ok}}},
	}
	for want, schemas := range cases {
		t.Run(want, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			if _, err := Open(path, schemas...); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want %q", err, want)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			if tableExists(t, raw, "schema_version") {
				t.Fatal("invalid schemas must be rejected before any migration")
			}
		})
	}
}

func TestModuleSchemaTableChecks(t *testing.T) {
	cases := map[string][]ModuleSchema{
		"do not create it": {{Module: "notes", Tables: []string{"notes_item"},
			Migrations: []func(*sql.Tx) error{createTable("notes_other", `id TEXT`)}}},
		"references notes table": {notesSchema(), {Module: "other", Tables: []string{"other_item"},
			Migrations: []func(*sql.Tx) error{createTable("other_item", `id TEXT REFERENCES notes_item(id)`)}}},
	}
	for want, schemas := range cases {
		t.Run(want, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			if _, err := Open(path, schemas...); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("err = %v, want %q", err, want)
			}
		})
	}
	// Core tables are shared infrastructure a module may reference.
	core := ModuleSchema{Module: "notes", Tables: []string{"notes_item"},
		Migrations: []func(*sql.Tx) error{createTable("notes_item", `id TEXT REFERENCES routine(id)`)}}
	openState(t, filepath.Join(t.TempDir(), "state.db"), core)
}

func TestModuleSchemaBackupFailureBlocksMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	openState(t, path, ModuleSchema{Module: "ledger-only"}).Close()
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	raw.SetMaxOpenConns(1)
	if err := raw.Ping(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Rename(dir, dir+"-moved"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Rename(dir+"-moved", dir) })
	err = migrateModules(raw, []ModuleSchema{notesSchema()}, latestSchemaVersion)
	if err == nil || !strings.Contains(err.Error(), "backup") {
		t.Fatalf("expected backup error, got %v", err)
	}
	if tableExists(t, raw, "notes_item") {
		t.Fatal("migration ran without a backup")
	}
}
