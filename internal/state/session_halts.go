package state

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	sessionHaltPrefix        = "session.halt.latest."
	sessionHaltReceiptPrefix = "session.halt.receipt."
	// Receipts only stop a replayed prompt from moving its session's halt
	// time. A prompt still pending after this long counts as a new halt.
	sessionHaltReceiptRetention = 30 * 24 * time.Hour
)

// RecordSessionHalt preserves the first observation of a request across
// reconnects and restarts. The existing KV table stores one receipt per prompt;
// recording a new receipt prunes those older than the retention window.
func (d *DB) RecordSessionHalt(ctx context.Context, sessionID, kind, requestID string, at int64) (int64, error) {
	if sessionID == "" || requestID == "" || (kind != "permission" && kind != "question") || at <= 0 {
		return 0, fmt.Errorf("invalid session halt")
	}
	identity, err := json.Marshal([]string{sessionID, kind, requestID})
	if err != nil {
		return 0, err
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var recorded int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO setting (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET key = excluded.key
		RETURNING updated_at`, sessionHaltReceiptPrefix+string(identity), sessionID, at).Scan(&recorded)
	if err != nil {
		return 0, err
	}
	if recorded == at {
		if _, err := tx.ExecContext(ctx, `DELETE FROM setting WHERE key >= ? AND key < ? AND updated_at < ?`,
			sessionHaltReceiptPrefix, "session.halt.receipt/", at-sessionHaltReceiptRetention.Milliseconds()); err != nil {
			return 0, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO setting (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET updated_at = MAX(setting.updated_at, excluded.updated_at)`,
		sessionHaltPrefix+sessionID, sessionID, recorded)
	if err != nil {
		return 0, err
	}
	return recorded, tx.Commit()
}

// SessionHalts reads owner-local halt receipts with an indexed prefix range.
func (d *DB) SessionHalts(ctx context.Context) (map[string]int64, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT value, MAX(updated_at) FROM setting
		WHERE key >= ? AND key < ? GROUP BY value`, sessionHaltPrefix, "session.halt.latest/")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]int64)
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		out[id] = at
	}
	return out, rows.Err()
}
