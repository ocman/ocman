package statev2

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
)

// RecentUserMessages returns retained, non-synthetic user prose oldest first.
// Ambiguous native references fail closed instead of mixing source histories.
func (d *DB) RecentUserMessages(ctx context.Context, owner, platform, id string, limit int) ([]string, error) {
	if owner == "" || platform == "" || id == "" || limit < 1 || limit > 100 {
		return nil, fmt.Errorf("statev2: invalid user intent scope")
	}
	var instance sql.NullString
	var count int
	err := d.reader().QueryRowContext(ctx, `SELECT count(*),min(s.instance_id) FROM session s JOIN instance i ON i.id=s.instance_id
		WHERE i.owner_id=? AND i.platform=? AND s.id=? AND s.source_deleted=0`, owner, platform, id).Scan(&count, &instance)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, ErrNotFound
	}
	if count != 1 {
		return nil, ErrConflict
	}
	rows, err := d.reader().QueryContext(ctx, `WITH selected AS (
		SELECT m.id,m.position FROM message m WHERE m.instance_id=? AND m.session_id=? AND m.role='user' AND m.source_deleted=0
		AND EXISTS (SELECT 1 FROM part p WHERE p.instance_id=m.instance_id AND p.session_id=m.session_id AND p.message_id=m.id
			AND p.kind='text' AND p.source_deleted=0 AND p.text!='' AND COALESCE(json_extract(p.extensions,'$.synthetic'),0)!=1)
		ORDER BY m.position DESC,m.id DESC LIMIT ?)
		SELECT m.id,p.text FROM selected m JOIN part p ON p.message_id=m.id AND p.instance_id=? AND p.session_id=?
		WHERE p.kind='text' AND p.source_deleted=0 AND COALESCE(json_extract(p.extensions,'$.synthetic'),0)!=1
		ORDER BY m.position DESC,m.id DESC,p.position,p.id`, instance.String, id, limit, instance.String, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	previous := ""
	for rows.Next() {
		var message, text string
		if err := rows.Scan(&message, &text); err != nil {
			return nil, err
		}
		if message != previous {
			out = append(out, "")
			previous = message
		}
		out[len(out)-1] += text
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out = slices.DeleteFunc(out, func(text string) bool { return text == "" })
	slices.Reverse(out)
	return out, nil
}
