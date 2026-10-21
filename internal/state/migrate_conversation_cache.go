package state

import "database/sql"

// The project index is derived conversation data, now persisted alongside the
// conversations. Do not copy the old cache: rebuilding avoids resurrecting
// projects after the user intentionally clears the conversation database.
func migrateConversationProjectCache(tx *sql.Tx) error {
	_, err := tx.Exec(`DROP TABLE IF EXISTS projects_cache`)
	return err
}
