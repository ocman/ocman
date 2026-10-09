package state

import (
	"context"
	"database/sql"
)

// Recovery checkpoints are immutable audit evidence, even after retry/cancel.
// A paused or cancelled Issue does not have to succeed to preserve its progress.
func latestFactoryCheckpoint(ctx context.Context, tx *sql.Tx, epicID, project string) (string, error) {
	var head string
	err := tx.QueryRowContext(ctx, `SELECT commit_sha FROM (
		SELECT json_extract(a.result_json, '$.commitSha') AS commit_sha, a.finished_at AS recorded_at, 1 AS priority, a.rowid AS sequence
		FROM factory_attempt a WHERE a.epic_id = ? AND json_extract(a.frozen_policy_json, '$.repository') = ?
		AND a.terminal_outcome = 'succeeded' AND json_valid(a.result_json) AND json_extract(a.result_json, '$.commitSha') <> ''
		UNION ALL
		SELECT json_extract(r.details_json, '$.commitSha'), r.created_at, 0, r.rowid
		FROM factory_audit_record r JOIN factory_attempt a ON a.id = r.attempt_id
		WHERE r.epic_id = ? AND json_extract(a.frozen_policy_json, '$.repository') = ?
		AND r.action = 'recovery.checkpoint' AND json_valid(r.details_json) AND json_extract(r.details_json, '$.commitSha') <> ''
	) ORDER BY recorded_at DESC, priority DESC, sequence DESC LIMIT 1`, epicID, project, epicID, project).Scan(&head)
	return head, err
}
