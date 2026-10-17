package pluginsmod

import (
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openPluginStore(t *testing.T) *Store {
	t.Helper()
	db, err := state.Open(statetest.Path(t), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

// A state.db at main's latest schema, and dev databases that ran this
// branch's old core numbering (plugin tables stamped v122, v123 or v124),
// all end with the plugin tables under the module schema. The legacy ones
// keep their rows and replay main's v122/v123 they skipped.
func TestSchemaUpgrades(t *testing.T) {
	for _, legacy := range []int{0, 122, 123, 124} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			path := statetest.Path(t)
			if legacy > 0 {
				db, err := state.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				raw := db.SQL()
				for _, stmt := range []string{
					`CREATE TABLE plugin_permission_profiles (id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK (trim(name) <> ''), rules_json TEXT NOT NULL)`,
					`INSERT INTO plugin_permission_profiles VALUES ('p', 'read-only', '[]')`,
					`ALTER TABLE archived_session DROP COLUMN snoozed_until`,
					`DELETE FROM schema_version WHERE version > 121`,
					fmt.Sprintf(`INSERT INTO schema_version VALUES (%d, 0)`, legacy),
				} {
					if _, err := raw.Exec(stmt); err != nil {
						t.Fatalf("%s: %v", stmt, err)
					}
				}
				_ = db.Close()
			}
			for range 2 { // Reopening after the upgrade is harmless.
				db, err := state.Open(path, Schema)
				if err != nil {
					t.Fatal(err)
				}
				raw := db.SQL()
				for _, query := range []string{
					`SELECT id FROM plugin_permission_profiles`,
					`SELECT session_id FROM plugin_session`,
					`SELECT id FROM plugin_session_outbox`,
					`SELECT snoozed_until FROM archived_session`,
				} {
					rows, err := raw.Query(query)
					if err != nil {
						t.Fatalf("%s: %v", query, err)
					}
					rows.Close()
				}
				var profiles, ledger int
				if err := raw.QueryRow(`SELECT (SELECT count(*) FROM plugin_permission_profiles),
					(SELECT max(version) FROM module_schema_version WHERE module = 'plugins')`).Scan(&profiles, &ledger); err != nil {
					t.Fatal(err)
				}
				if want := min(legacy, 1); profiles != want || ledger != len(Schema.Migrations) {
					t.Fatalf("profiles = %d (want %d), ledger = %d", profiles, want, ledger)
				}
				_ = db.Close()
			}
		})
	}
}
