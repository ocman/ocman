package state

import "context"

// SessionInterruption is an immutable history entry keyed to the affected turn.
type SessionInterruption struct {
	MessageID  string
	ObservedAt int64
	Message    string
	// BaselineStatus is only used by replacement preparation, not history.
	BaselineStatus         string
	BaselineMessageCreated int64
	BaselineErrorName      string
}

func (d *DB) RecordSessionInterruption(ctx context.Context, platform, sessionID string, notice SessionInterruption) error {
	return d.RecordSessionInterruptions(ctx, platform, map[string]SessionInterruption{sessionID: notice})
}

// RecordSessionInterruptions commits one replacement's affected turns together.
func (d *DB) RecordSessionInterruptions(ctx context.Context, platform string, notices map[string]SessionInterruption) error {
	_, err := d.writeInterruptionBatch(ctx, platform, "", notices, true)
	return err
}

func (d *DB) writeInterruptionBatch(ctx context.Context, platform, root string, notices map[string]SessionInterruption, confirmed bool) ([]string, error) {
	if len(notices) == 0 && confirmed && root == "" {
		return nil, nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if confirmed && root != "" {
		if err := retireReplacementRuntime(ctx, tx, platform, root); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE session_replacement SET phase = 'confirmed', baseline_json = '{}', stop_baseline_json = '{}', stop_started_at = 0, stop_runtime_json = '',admission_started_at=0,reconciliation_initialized=0
			WHERE platform = ? AND replacement_root = ?`, platform, root); err != nil {
			return nil, err
		}
		if err := clearReplacementWork(ctx, tx, platform, root); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM session_interruption WHERE platform = ? AND replacement_root = ? AND confirmed = 0`, platform, root); err != nil {
			return nil, err
		}
	}
	var ids []string
	for sessionID, notice := range notices {
		result, err := tx.ExecContext(ctx, `INSERT INTO session_interruption
			(platform, session_id, message_id, observed_at, message, replacement_root, confirmed) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(platform, session_id, message_id) DO UPDATE SET
			observed_at = excluded.observed_at, message = excluded.message,
			replacement_root = excluded.replacement_root, confirmed = excluded.confirmed
			WHERE session_interruption.confirmed = 0 AND excluded.confirmed = 1`, platform, sessionID, notice.MessageID, notice.ObservedAt, notice.Message, root, confirmed)
		if err != nil {
			return nil, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if changed > 0 {
			ids = append(ids, sessionID)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return ids, nil
}

// ConfirmSessionInterruptions publishes only the preparation whose server was
// successfully stopped. It returns identities to broadcast after the commit.
func (d *DB) ConfirmSessionInterruptions(ctx context.Context, platform, root string, final map[string]SessionInterruption) ([]string, error) {
	return d.writeInterruptionBatch(ctx, platform, root, final, true)
}

func (d *DB) SessionInterruptions(ctx context.Context, platform, sessionID string) ([]SessionInterruption, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT message_id, observed_at, message FROM session_interruption
		WHERE platform = ? AND session_id = ? AND confirmed = 1 ORDER BY observed_at, message_id`, platform, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var notices []SessionInterruption
	for rows.Next() {
		var notice SessionInterruption
		if err := rows.Scan(&notice.MessageID, &notice.ObservedAt, &notice.Message); err != nil {
			return nil, err
		}
		notices = append(notices, notice)
	}
	return notices, rows.Err()
}
