package state

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"
)

func migrateSnooze(tx *sql.Tx) error {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='archived_session')`).Scan(&exists); err != nil || !exists {
		return err
	}
	return addColumnIfMissing(tx, "archived_session", "snoozed_until", "INTEGER NOT NULL DEFAULT 0")
}

// SnoozeSession hides a conversation until the hub-clock deadline. Activity
// cannot resurface it early; opening or explicitly archiving it cancels snooze.
func (d *DB) SnoozeSession(ctx context.Context, platform, sessionID string, until int64) error {
	if until <= time.Now().UnixMilli() {
		return fmt.Errorf("snooze time must be in the future")
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO archived_session
		(platform, session_id, session_time_updated, archived_at, snoozed_until)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			session_time_updated = excluded.session_time_updated,
			archived_at = excluded.archived_at, snoozed_until = excluded.snoozed_until`,
		platform, sessionID, int64(math.MaxInt64), time.Now().UnixMilli(), until)
	return err
}

// SnoozedSessions returns deadlines keyed by owner-qualified session identity.
func (d *DB) SnoozedSessions(ctx context.Context) (map[Key]int64, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT platform, session_id, snoozed_until FROM archived_session WHERE snoozed_until > 0`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deadlines := make(map[Key]int64)
	for rows.Next() {
		var key Key
		var until int64
		if err := rows.Scan(&key.Platform, &key.SessionID, &until); err != nil {
			return nil, err
		}
		deadlines[key] = until
	}
	return deadlines, rows.Err()
}

// WakeSnoozedSessions atomically removes due archives and protects the woken
// sessions from inactivity-based auto-archive. Deadlines survive restarts.
func (d *DB) WakeSnoozedSessions(ctx context.Context, now int64) ([]Key, error) {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.QueryContext(ctx, `DELETE FROM archived_session
		WHERE snoozed_until > 0 AND snoozed_until <= ? RETURNING platform, session_id`, now)
	if err != nil {
		return nil, err
	}
	var keys []Key
	for rows.Next() {
		var key Key
		if err := rows.Scan(&key.Platform, &key.SessionID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		keys = append(keys, key)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `INSERT INTO unarchived_entity
			(kind, remote_id, entity_key, unarchived_at) VALUES ('session', 'local', ?, ?)
			ON CONFLICT(kind, remote_id, entity_key) DO UPDATE SET unarchived_at = excluded.unarchived_at`,
			sessionKey(key.Platform, key.SessionID), now); err != nil {
			return nil, err
		}
	}
	return keys, tx.Commit()
}
