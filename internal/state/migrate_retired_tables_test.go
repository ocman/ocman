package state

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
)

// beforeRetiredTables is the last schema that still has the retired tables.
const beforeRetiredTables = 127

// schemaObjects lists "type name" for every object not owned by a retired
// table, or every object when keepRetired is set.
func schemaObjects(t *testing.T, db *sql.DB, keepRetired bool) []string {
	t.Helper()
	query := `SELECT type || ' ' || name FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'`
	if !keepRetired {
		query += ` AND tbl_name NOT IN (SELECT value FROM json_each(?))`
	}
	retired, err := json.Marshal(retiredTables)
	if err != nil {
		t.Fatal(err)
	}
	args := []any{}
	if !keepRetired {
		args = append(args, string(retired))
	}
	rows, err := db.Query(query+` ORDER BY type, name`, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			t.Fatal(err)
		}
		out = append(out, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRetiredTablesMigrationDropsOnlyRetiredTables(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrateTo(raw, beforeRetiredTables); err != nil {
		t.Fatal(err)
	}
	before, want := schemaObjects(t, raw, true), schemaObjects(t, raw, false)
	for _, table := range retiredTables {
		if !slices.Contains(before, "table "+table) {
			t.Errorf("v%d lacks retired table %s", beforeRetiredTables, table)
		}
	}
	// The circular workflow_definition <-> workflow_version references and
	// a child row must not block the drop, even with enforcement on.
	if _, err := raw.Exec(`PRAGMA foreign_keys = ON;
		INSERT INTO workflow_definition (id, name, current_revision, created_at, updated_at) VALUES ('w', 'W', 1, 1, 1);
		INSERT INTO workflow_version (id, workflow_id, name, revision, metadata_version, definition_json, concurrency, created_at) VALUES ('v', 'w', 'W', 1, '1', '{}', 1, 1);
		UPDATE workflow_definition SET active_version_id = 'v';
		INSERT INTO workflow_run (id, workflow_id, version_id, state, created_at, updated_at) VALUES ('r', 'w', 'v', 'active', 1, 1);
		INSERT INTO setting (key, value, updated_at) VALUES ('kept', 'yes', 1)`); err != nil {
		t.Fatal(err)
	}
	// Compare only the retirement step; later migrations may add new tables.
	if err := migrateTo(raw, beforeRetiredTables+1); err != nil {
		t.Fatal(err)
	}
	after := schemaObjects(t, raw, true)
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(after, want) {
		t.Fatalf("schema after drop:\n got %v\nwant %v", after, want)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if value, ok, err := db.GetSetting(t.Context(), "kept"); err != nil || !ok || value != "yes" {
		t.Fatalf("unrelated row lost: %q %v %v", value, ok, err)
	}

	fresh, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fresh.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := migrate(fresh); err != nil {
		t.Fatal(err)
	}
	upgraded := schemaObjects(t, db.db, true)
	if got := schemaObjects(t, fresh, true); !slices.Equal(got, upgraded) {
		t.Fatalf("fresh schema differs from upgraded schema:\n fresh %v\nupgrade %v", got, upgraded)
	}
}
