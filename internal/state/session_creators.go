package state

import (
	"context"
	"encoding/json"
)

// SessionCreators reads provenance for a displayed page in one query. Explicit
// compound keys prevent a remote session from borrowing local provenance.
func (d *DB) SessionCreators(ctx context.Context, keys []Key) (map[Key]SessionCreator, error) {
	out := make(map[Key]SessionCreator)
	if len(keys) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(keys)
	if err != nil {
		return nil, err
	}
	rows, err := d.db.QueryContext(ctx, `SELECT c.platform, c.session_id, c.creator_platform, c.creator_session_id, c.created_at
		FROM json_each(?) k JOIN session_creator c
		ON c.platform=json_extract(k.value, '$.Platform') AND c.session_id=json_extract(k.value, '$.SessionID')`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c SessionCreator
		if err := rows.Scan(&c.Platform, &c.SessionID, &c.CreatorPlatform, &c.CreatorSessionID, &c.CreatedAt); err != nil {
			return nil, err
		}
		out[Key{Platform: c.Platform, SessionID: c.SessionID}] = c
	}
	return out, rows.Err()
}
