package state

import "database/sql"

func migrateQueuePreserveSelections(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='queued_message')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	} // Partial legacy databases may omit unrelated tables.
	return addColumnIfMissing(tx, "queued_message", "preserve_selections", "INTEGER NOT NULL DEFAULT 0")
}
