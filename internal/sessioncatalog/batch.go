package sessioncatalog

import (
	"context"
	"encoding/json"
)

// GetMany reads exact canonical identities in one indexed query. Missing IDs
// are omitted. Callers must validate ownership and publication just as for Get.
func (s *Store) GetMany(ctx context.Context, ids []string) (map[string]Session, error) {
	out := make(map[string]Session, len(ids))
	if len(ids) == 0 {
		return out, ctx.Err()
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+columns+` FROM session_catalog
		WHERE id IN (SELECT value FROM json_each(?))`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out[row.ID] = row
	}
	return out, rows.Err()
}
