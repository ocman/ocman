package db

import "context"

// GetSessionStatus infers status from only the latest message. Select its ID
// before extracting JSON so historical message blobs are never loaded.
func (d *DB) GetSessionStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
	var role, finish, lastError string
	err := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(json_extract(m.data, '$.role'), ''),
		       COALESCE(json_extract(m.data, '$.finish'), ''),
		       COALESCE(json_extract(m.data, '$.error'), '')
		FROM session s
		LEFT JOIN message m ON m.id = (
			SELECT id FROM message WHERE session_id = s.id
			ORDER BY time_created DESC, id DESC LIMIT 1
		)
		WHERE s.id = ?
	`, sessionID).Scan(&role, &finish, &lastError)
	if err != nil {
		return "", err
	}
	return InferSessionStatus(role, finish, lastError, false), nil
}

// StatusCandidateDirectories returns the distinct directories that may hold a
// running turn: any session updated at or after sinceMs, plus any worktree
// session, however old, whose latest message finished with "tool-calls" or
// "unknown". OpenCode's prompt loop treats those finishes as non-terminal
// (the turn continues, e.g. blocked on a permission), so age proves nothing.
// It reads the tables directly, never the cached snapshot. The finish check
// is limited to worktree paths: root directories are read unscoped anyway,
// and the limit keeps the per-session JSON lookup off most rows.
func (d *DB) StatusCandidateDirectories(ctx context.Context, sinceMs int64) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT s.directory FROM session s
		WHERE s.time_updated >= ?
		   OR (s.directory LIKE '%/.worktrees/%' AND (
			SELECT json_extract(m.data, '$.finish') FROM message m
			WHERE m.session_id = s.id
			ORDER BY m.time_created DESC, m.id DESC LIMIT 1
		   ) IN ('tool-calls', 'unknown'))`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var directory string
		if err := rows.Scan(&directory); err != nil {
			return nil, err
		}
		out = append(out, directory)
	}
	return out, rows.Err()
}
