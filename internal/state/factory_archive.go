package state

import (
	"context"
	"database/sql"
	"fmt"
)

// FactorySessions maps each session a Factory attempt ran in to that attempt's
// ID, so the session list can tag Factory sessions without the Epic payload.
// ponytail: full scan of factory_attempt per session list; index (session_platform, session_id) if attempts grow large.
func (d *DB) FactorySessions(ctx context.Context) (map[Key]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT session_platform, session_id, id FROM factory_attempt
		WHERE session_id <> '' ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("listing Factory sessions: %w", err)
	}
	defer rows.Close()
	sessions := make(map[Key]string)
	for rows.Next() {
		var key Key
		var attemptID string
		if err := rows.Scan(&key.Platform, &key.SessionID, &attemptID); err != nil {
			return nil, fmt.Errorf("scanning Factory session: %w", err)
		}
		sessions[key] = attemptID
	}
	return sessions, rows.Err()
}

// archiveFactorySessionsTx archives the sessions of the Factory attempts
// matched by where (a predicate over factory_attempt). The stored update time
// is MaxInt64 so an agent's trailing reply cannot auto-unarchive the session.
func archiveFactorySessionsTx(ctx context.Context, tx *sql.Tx, now int64, where string, args ...any) error {
	selected := `SELECT session_platform, session_id FROM factory_attempt WHERE session_id <> '' AND (` + where + `)`
	if _, err := tx.ExecContext(ctx, `INSERT INTO archived_session (platform, session_id, session_time_updated, archived_at)
		SELECT session_platform, session_id, 9223372036854775807, ? FROM (`+selected+`) WHERE true
		ON CONFLICT(platform, session_id) DO UPDATE SET session_time_updated = excluded.session_time_updated, archived_at = excluded.archived_at`,
		append([]any{now}, args...)...); err != nil {
		return fmt.Errorf("archiving Factory sessions: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM unarchived_entity WHERE kind = 'session' AND remote_id = 'local'
		AND entity_key IN (SELECT session_platform || char(0) || session_id FROM (`+selected+`))`, args...); err != nil {
		return fmt.Errorf("clearing Factory session unarchive: %w", err)
	}
	return nil
}
