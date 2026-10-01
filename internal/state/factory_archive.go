package state

import (
	"context"
	"database/sql"
	"fmt"
)

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
