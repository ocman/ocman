// Package platformconnections stores owner-local named connection identities.
// It does not launch agents, inspect credentials or authorize execution.
package platformconnections

import (
	"database/sql"

	"github.com/ocman/ocman/internal/state"
)

var Schema = state.ModuleSchema{
	Module: "platform-connections",
	Tables: []string{"platform_connection", "platform_connection_collection", "platform_connection_setup", "platform_connection_session", "platform_connection_creation", "platform_connection_runtime", "platform_connection_source"},
	Migrations: []func(*sql.Tx) error{func(tx *sql.Tx) error {
		_, err := tx.Exec(`CREATE TABLE platform_connection (
			id TEXT PRIMARY KEY NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('opencode','claudecode','codex')),
			name TEXT NOT NULL CHECK(length(name)>0),
			configuration TEXT NOT NULL CHECK(configuration IN ('existing','isolated')),
			directory TEXT NOT NULL,
			enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
			removed INTEGER NOT NULL DEFAULT 0 CHECK(removed IN (0,1)),
			legacy_kind TEXT UNIQUE CHECK(legacy_kind IS NULL OR (legacy_kind=kind AND configuration='existing')),
			CHECK(removed=0 OR enabled=0)
		);
		CREATE TABLE platform_connection_collection (
			id INTEGER PRIMARY KEY CHECK(id=1),
			onboarding_complete INTEGER NOT NULL CHECK(onboarding_complete IN (0,1)),
			default_id TEXT REFERENCES platform_connection(id)
		);`)
		return err
	}, migrateSetupReservations, migrateSessionConnections, migrateCreationConnections, migrateConnectionRuntimes, migrateConnectionSources},
}

func migrateSetupReservations(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE platform_connection_setup (
		id TEXT PRIMARY KEY NOT NULL,
		connection_id TEXT NOT NULL UNIQUE,
		kind TEXT NOT NULL CHECK(kind IN ('opencode','claudecode','codex')),
		configuration TEXT NOT NULL CHECK(configuration IN ('existing','isolated')),
		directory TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN ('pending','saved','canceled'))
	);
	CREATE INDEX platform_connection_setup_pending ON platform_connection_setup(status) WHERE status='pending';`)
	return err
}
