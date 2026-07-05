package state

import (
	"fmt"
	"time"
)

// MarkSessionSeen records the latest session update the user has
// viewed for the given platform/session. Per-platform: Claude Code's
// session "abc123" and OpenCode's session "abc123" are independent.
func (d *DB) MarkSessionSeen(platform, sessionID string, sessionTimeUpdated int64) error {
	_, err := d.db.Exec(`
		INSERT INTO seen_session (platform, session_id, session_time_updated, seen_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(platform, session_id) DO UPDATE SET
			session_time_updated = CASE
				WHEN excluded.session_time_updated > seen_session.session_time_updated THEN excluded.session_time_updated
				ELSE seen_session.session_time_updated
			END,
			seen_at = excluded.seen_at
	`, platform, sessionID, sessionTimeUpdated, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("marking session seen: %w", err)
	}
	return nil
}

// SeenSessions returns every seen session's time_updated, keyed by
// (platform, session-id). Callers doing a per-platform lookup can
// construct a Key directly.
func (d *DB) SeenSessions() (map[Key]int64, error) {
	rows, err := d.db.Query(`SELECT platform, session_id, session_time_updated FROM seen_session`)
	if err != nil {
		return nil, fmt.Errorf("listing seen sessions: %w", err)
	}
	defer rows.Close()

	seen := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var sessionTimeUpdated int64
		if err := rows.Scan(&platform, &sessionID, &sessionTimeUpdated); err != nil {
			return nil, fmt.Errorf("scanning seen session: %w", err)
		}
		seen[Key{Platform: platform, SessionID: sessionID}] = sessionTimeUpdated
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading seen sessions: %w", err)
	}

	return seen, nil
}
