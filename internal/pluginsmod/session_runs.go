package pluginsmod

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/ocman/ocman/internal/state"
	plugins "github.com/ocman/ocman/sdk/protocol"
)

func migratePluginSessionRuns(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE plugin_session_run (
		plugin_id TEXT NOT NULL, operation_id TEXT NOT NULL,
		fingerprint BLOB NOT NULL, session_id TEXT NOT NULL DEFAULT '',
		error_category TEXT NOT NULL DEFAULT '', settled INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(plugin_id, operation_id)
	)`)
	return err
}

// reserveSessionRun fences creation across concurrent callers and restarts.
// A pending receipt is uncertain work, never permission to start another run.
func (d *Store) reserveSessionRun(ctx context.Context, pluginID, operationID, project string, params plugins.SessionRunParams) (sessionID string, claimed bool, err error) {
	data, _ := json.Marshal(struct {
		Project string
		Params  plugins.SessionRunParams
	}{project, params})
	hash := sha256.Sum256(data)
	result, err := d.db.ExecContext(ctx, `INSERT INTO plugin_session_run (plugin_id,operation_id,fingerprint)
		VALUES (?,?,?) ON CONFLICT DO NOTHING`, pluginID, operationID, hash[:])
	if err != nil {
		return "", false, state.ErrPluginState
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", false, state.ErrPluginState
	}
	if n == 1 {
		return "", true, nil
	}
	var stored []byte
	var category plugins.ErrorCategory
	var settled bool
	if err := d.db.QueryRowContext(ctx, `SELECT fingerprint,session_id,error_category,settled FROM plugin_session_run
		WHERE plugin_id=? AND operation_id=?`, pluginID, operationID).Scan(&stored, &sessionID, &category, &settled); err != nil {
		return "", false, state.ErrPluginState
	}
	if string(stored) != string(hash[:]) {
		return "", false, &plugins.WireError{Category: plugins.ErrorConflict}
	}
	if !settled {
		return "", false, plugins.ErrUnavailable
	}
	if category != "" {
		return "", false, &plugins.WireError{Category: category}
	}
	return sessionID, false, nil
}

func (d *Store) finishSessionRun(ctx context.Context, pluginID, operationID, sessionID string, runErr error) error {
	// Once creation was admitted, its receipt must survive caller cancellation.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var category plugins.ErrorCategory
	if runErr != nil {
		category = plugins.RequestError(runErr).Category
	}
	_, err := d.db.ExecContext(ctx, `UPDATE plugin_session_run SET session_id=?,error_category=?,settled=1
		WHERE plugin_id=? AND operation_id=? AND settled=0`, sessionID, category, pluginID, operationID)
	if err != nil {
		return state.ErrPluginState
	}
	return nil
}
