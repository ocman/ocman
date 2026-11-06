package state

import "database/sql"

func migrateWebhookProject(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='webhook_subscription')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "webhook_subscription", "project_pointer", "TEXT NOT NULL DEFAULT ''")
}
