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
