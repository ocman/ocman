package claudeprovider

import (
	"context"
	"database/sql"
	"errors"
)

// contextTokens reads the latest assistant usage independently of message
// paging. Failed or unfinished messages with no output leave the last valid
// snapshot intact, matching the OpenCode adapter's context count.
func (x *Index) contextTokens(ctx context.Context, id string) (int64, error) {
	var tokens int64
	err := x.db.QueryRowContext(ctx, `SELECT
		COALESCE(json_extract(data, '$.tokens.input'), 0) +
		COALESCE(json_extract(data, '$.tokens.output'), 0) +
		COALESCE(json_extract(data, '$.tokens.reasoning'), 0) +
		COALESCE(json_extract(data, '$.tokens.cache.read'), 0) +
		COALESCE(json_extract(data, '$.tokens.cache.write'), 0)
		FROM usage WHERE session_id = ?
		AND json_extract(data, '$.role') = 'assistant'
		AND json_extract(data, '$.tokens.output') > 0
		ORDER BY time_created DESC, id DESC LIMIT 1`, id).Scan(&tokens)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return tokens, err
}
