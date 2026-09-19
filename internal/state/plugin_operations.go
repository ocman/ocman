package state

import (
	"context"
	"database/sql"

	"github.com/NoUseFreak/ocman/internal/plugins"
)

func migrateToV92(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS plugin_action_operation (
		plugin_id TEXT NOT NULL,
		operation_id TEXT NOT NULL,
		PRIMARY KEY(plugin_id, operation_id)
	)`)
	return err
}

// ReservePluginOperation records admission before writing to a plugin. Receipts
// contain no context or results and survive removal/restarts. An uncertain outcome
// is never replayed. The broker caches terminal responses for the current host life.
// This method does not acquire pluginMu: action admission already holds it.
func (d *DB) ReservePluginOperation(ctx context.Context, pluginID, operationID string) error {
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_action_operation(plugin_id,operation_id) VALUES(?,?) ON CONFLICT DO NOTHING`, pluginID, operationID)
	if err != nil {
		return ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return ErrPluginState
	}
	if n == 0 {
		return &plugins.WireError{Category: plugins.ErrorConflict}
	}
	return nil
}
