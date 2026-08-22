package state

import (
	"context"
	"fmt"
	"time"
)

// PinSession marks a session as pinned. Idempotent: repeated calls
// are no-ops (pinned_at is not updated).
func (d *DB) PinSession(ctx context.Context, platform, sessionID string) error {
	_, err := d.db.ExecContext(ctx, `
		INSERT INTO pinned_session (platform, session_id, pinned_at)
		VALUES (?, ?, ?)
		ON CONFLICT(platform, session_id) DO NOTHING
	`, platform, sessionID, time.Now().UnixMilli())
	if err != nil {
		return fmt.Errorf("pinning session: %w", err)
	}
	return nil
}

// UnpinSession removes a session's pinned marker.
func (d *DB) UnpinSession(ctx context.Context, platform, sessionID string) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM pinned_session WHERE platform = ? AND session_id = ?`,
		platform, sessionID,
	)
	if err != nil {
		return fmt.Errorf("unpinning session: %w", err)
	}
	return nil
}

// PinnedSessions returns every pinned session's pinned_at timestamp,
// keyed by (platform, session-id).
func (d *DB) PinnedSessions(ctx context.Context) (map[Key]int64, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT platform, session_id, pinned_at FROM pinned_session`)
	if err != nil {
		return nil, fmt.Errorf("listing pinned sessions: %w", err)
	}
	defer rows.Close()

	pinned := make(map[Key]int64)
	for rows.Next() {
		var platform, sessionID string
		var pinnedAt int64
		if err := rows.Scan(&platform, &sessionID, &pinnedAt); err != nil {
			return nil, fmt.Errorf("scanning pinned session: %w", err)
		}
		pinned[Key{Platform: platform, SessionID: sessionID}] = pinnedAt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading pinned sessions: %w", err)
	}

	return pinned, nil
}
