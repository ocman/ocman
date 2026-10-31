package pluginsmod

import (
	"database/sql"
	"time"

	"github.com/ocman/ocman/internal/state"
)

// Schema owns the plugin tables added after plugin registrations moved to
// the core sequence: named permission profiles and the session.v1 mapping
// with its completed-turn outbox. Every statement is IF NOT EXISTS because
// development databases already created these tables as core v122-v124
// before main took those numbers.
var Schema = state.ModuleSchema{
	Module: "plugins",
	Tables: []string{"plugin_permission_profiles", "plugin_session", "plugin_session_outbox", "plugin_session_run", "plugin_session_project"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_permission_profiles (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL UNIQUE COLLATE NOCASE CHECK (trim(name) <> ''),
			rules_json TEXT NOT NULL
		)`); err != nil {
			return err
		}
		return migratePluginSession(tx)
	}, migratePluginSessionRuns, migratePluginSessionUsage, migratePluginSessionProjects, migratePluginSessionProjectReservations},
}

// sessionDoneRetention keeps delivered session receipts as long as the
// conversation outbox keeps its own.
const sessionDoneRetention = 24 * time.Hour

// Store is the repository for Schema's tables.
type Store struct{ db *sql.DB }

// NewStore reads Schema's tables from an open state database.
func NewStore(d *state.DB) *Store { return &Store{db: d.SQL()} }
