package state

import "database/sql"

// v129 keeps context-dependent judge decisions out of inherited allow rules.
func migrateContextBoundApprovals(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='auto_approved_permission')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	return addColumnIfMissing(tx, "auto_approved_permission", "context_bound", "INTEGER NOT NULL DEFAULT 0")
}
