package state

import (
	"context"
	"fmt"
	"time"
)

// MarkSessionSeen records the latest session update the user has
// viewed for the given platform/session. Per-platform: two platforms'
// session "abc123" are tracked independently.
// interrupted acknowledges the interruption itself, which writes no new activity.
func (d *DB) MarkSessionSeen(ctx context.Context, platform, sessionID string, sessionTimeUpdated int64, interrupted ...bool) error {
	seenInterrupted := len(interrupted) > 0 && interrupted[0]
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO seen_session (platform, session_id, session_time_updated, seen_at, interrupted)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			interrupted = CASE
				WHEN excluded.session_time_updated > seen_session.session_time_updated THEN excluded.interrupted
				WHEN excluded.session_time_updated = seen_session.session_time_updated THEN seen_session.interrupted OR excluded.interrupted
				ELSE seen_session.interrupted
			END,
			session_time_updated = CASE
				WHEN excluded.session_time_updated > seen_session.session_time_updated THEN excluded.session_time_updated
				ELSE seen_session.session_time_updated
			END,
			seen_at = excluded.seen_at
	`, platform, sessionID, sessionTimeUpdated, time.Now().UnixMilli(), seenInterrupted)
	if err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	return nil
}

// SeenSessions returns every seen session's time_updated, keyed by
// (platform, session-id). Callers doing a per-platform lookup can
// construct a Key directly.
func (d *DB) SeenSessions(ctx context.Context) (map[Key]int64, error) {
	records, err := d.SeenSessionStates(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[Key]int64, len(records))
	for key, record := range records {
		seen[key] = record.TimeUpdated
	}
	return seen, nil
}

// SeenSessionState distinguishes reading a running turn from seeing it crash.
type SeenSessionState struct {
	TimeUpdated int64
	Interrupted bool
}

func (d *DB) SeenSessionStates(ctx context.Context) (map[Key]SeenSessionState, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT platform, session_id, session_time_updated, interrupted FROM seen_session`)
	if err != nil {
		return nil, fmt.Errorf("listing seen sessions: %w", err)
	}
	defer rows.Close()

	seen := make(map[Key]SeenSessionState)
	for rows.Next() {
		var platform, sessionID string
		var record SeenSessionState
		if err := rows.Scan(&platform, &sessionID, &record.TimeUpdated, &record.Interrupted); err != nil {
			return nil, fmt.Errorf("scanning seen session: %w", err)
		}
		seen[Key{Platform: platform, SessionID: sessionID}] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading seen sessions: %w", err)
	}

	return seen, nil
}
