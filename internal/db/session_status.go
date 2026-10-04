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

// RecentSessionDirectories returns the distinct directories of sessions
// updated at or after sinceMs. It reads the session table directly, never
// the cached snapshot, so a turn started moments ago is included.
func (d *DB) RecentSessionDirectories(ctx context.Context, sinceMs int64) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `SELECT DISTINCT directory FROM session WHERE time_updated >= ?`, sinceMs)
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
