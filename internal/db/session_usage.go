package db

import (
	"context"
	"database/sql"
)

// Keep descendants first: SQLite otherwise scans the entire message table per run.
const descendantMessagesQuery = `WITH RECURSIVE descendants(id) AS (
		SELECT id FROM session WHERE id = ?
		UNION
		SELECT s.id FROM session s JOIN descendants d ON s.parent_id = d.id
	)
	SELECT m.session_id, CASE WHEN json_valid(m.data) THEN json_object(
		'role', json_extract(m.data, '$.role'),
		'providerID', json_extract(m.data, '$.providerID'),
		'modelID', json_extract(m.data, '$.modelID'),
		'cost', json_extract(m.data, '$.cost'),
		'tokens', json_extract(m.data, '$.tokens')
	) END FROM descendants d CROSS JOIN message m WHERE m.session_id = d.id`

// GetDescendantMessages reads only message metadata for usage accounting.
// UNION also bounds traversal if corrupt parent links contain a cycle.
func (d *DB) GetDescendantMessages(ctx context.Context, sessionID string) ([]Message, error) {
	var exists int
	if err := d.db.QueryRowContext(ctx, `SELECT 1 FROM session WHERE id = ?`, sessionID).Scan(&exists); err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, descendantMessagesQuery, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		var message Message
		var raw sql.NullString
		if err := rows.Scan(&message.SessionID, &raw); err != nil {
			return nil, err
		}
		message.Data = []byte(raw.String)
		messages = append(messages, message)
	}
	return messages, rows.Err()
}
