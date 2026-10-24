package pluginsmod

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func openPluginStore(t *testing.T) *Store {
	t.Helper()
	db, err := state.Open(statetest.Path(t, Schema), Schema)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewStore(db)
}

// A state.db at main's latest schema ends with the plugin tables under the
// module schema.
func TestSchemaUpgrades(t *testing.T) {
	path := statetest.Path(t)
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
		} {
			rows, err := raw.Query(query)
			if err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			rows.Close()
		}
		var ledger int
		if err := raw.QueryRow(`SELECT max(version) FROM module_schema_version WHERE module = 'plugins'`).Scan(&ledger); err != nil {
			t.Fatal(err)
		}
		if ledger != len(Schema.Migrations) {
			t.Fatalf("ledger = %d, want %d", ledger, len(Schema.Migrations))
		}
		_ = db.Close()
	}
}
