package db

import "context"

// GetSessionMessageStatus infers status from only the newest message. Idle
// events must not parse the session's historical message blobs.
func (d *DB) GetSessionMessageStatus(ctx context.Context, sessionID string) (SessionStatus, error) {
	var role, finish, messageError string
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
	`, sessionID).Scan(&role, &finish, &messageError)
	if err != nil {
		return "", err
	}
	return InferSessionStatus(role, finish, messageError, false), nil
}
