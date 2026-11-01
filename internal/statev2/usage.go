package statev2

import (
	"context"
	"math"
)

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
	return usage(ctx, d.reader(), key)
}

func usage(ctx context.Context, q rowQuerier, key Key) (Usage, error) {
	var u Usage
	err := q.QueryRowContext(ctx, `SELECT count(*), COALESCE(sum(cost),0), COALESCE(sum(tokens_input),0),
		COALESCE(sum(tokens_output),0), COALESCE(sum(tokens_reasoning),0), COALESCE(sum(tokens_cache_read),0),
		COALESCE(sum(tokens_cache_write),0) FROM message WHERE instance_id=? AND session_id=? AND source_deleted=0`,
		key.InstanceID, key.SessionID).
		Scan(&u.Messages, &u.Cost, &u.TokensInput, &u.TokensOutput, &u.TokensReasoning, &u.TokensCacheRead, &u.TokensCacheWrite)
	return u, err
}

// Matches compares costs with a tolerance because SQLite sums in its own
// order, which can differ from the order used to compute the expectation.
func (u Usage) Matches(got Usage) bool {
	tolerance := 1e-9 * math.Max(1, math.Abs(u.Cost))
	return u.Messages == got.Messages &&
		math.Abs(u.Cost-got.Cost) <= tolerance &&
		u.TokensInput == got.TokensInput && u.TokensOutput == got.TokensOutput &&
		u.TokensReasoning == got.TokensReasoning &&
		u.TokensCacheRead == got.TokensCacheRead && u.TokensCacheWrite == got.TokensCacheWrite
}
