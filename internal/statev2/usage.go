package statev2

import "context"

// Usage totals the stored message accounting of one session. It excludes
// source-deleted messages, matching what a complete source snapshot contains.
type Usage struct {
	Messages         int64
	Cost             float64
	TokensInput      int64
	TokensOutput     int64
	TokensReasoning  int64
	TokensCacheRead  int64
	TokensCacheWrite int64
}

func (d *DB) Usage(ctx context.Context, key Key) (Usage, error) {
	var u Usage
	err := d.db.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(cost),0), COALESCE(sum(tokens_input),0),
		COALESCE(sum(tokens_output),0), COALESCE(sum(tokens_reasoning),0), COALESCE(sum(tokens_cache_read),0),
		COALESCE(sum(tokens_cache_write),0) FROM message WHERE instance_id=? AND session_id=? AND source_deleted=0`,
		key.InstanceID, key.SessionID).
		Scan(&u.Messages, &u.Cost, &u.TokensInput, &u.TokensOutput, &u.TokensReasoning, &u.TokensCacheRead, &u.TokensCacheWrite)
	return u, err
}
