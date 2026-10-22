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
	if len(notices) == 0 {
		return nil
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for sessionID, notice := range notices {
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_interruption
			(platform, session_id, message_id, observed_at, message, replacement_root, confirmed) VALUES (?, ?, ?, ?, ?, '', 1)
			ON CONFLICT(platform, session_id, message_id) DO UPDATE SET
			observed_at = excluded.observed_at, message = excluded.message,
			replacement_root = excluded.replacement_root, confirmed = excluded.confirmed
			WHERE session_interruption.confirmed = 0 AND excluded.confirmed = 1`, platform, sessionID, notice.MessageID, notice.ObservedAt, notice.Message); err != nil {
			return err
		}
	}
	return tx.Commit()
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
