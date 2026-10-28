package conversationingest

import (
	"context"
	"math"

	"github.com/ocman/ocman/internal/statev2"
)

// usageMatches reports whether the stored accounting for a session equals the
// totals of the snapshot just read. A false result means a column mapping or
// normalization bug, or content that changed after the read.
func (w *sourceWorker) usageMatches(ctx context.Context, sessionID string, want statev2.Usage) (bool, error) {
	got, err := w.module.deps.Store.Usage(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: sessionID})
	if err != nil {
		return false, err
	}
	return sameUsage(want, got), nil
}

// expectedUsage totals a normalized batch the way Usage reads it back, so a
// verification pass can detect a column that did not receive its value.
func expectedUsage(batch statev2.Batch) statev2.Usage {
	var u statev2.Usage
	for _, m := range batch.Messages {
		u.Messages++
		u.Cost += m.Cost
		u.TokensInput += m.TokensInput
		u.TokensOutput += m.TokensOutput
		u.TokensReasoning += m.TokensReasoning
		u.TokensCacheRead += m.TokensCacheRead
		u.TokensCacheWrite += m.TokensCacheWrite
	}
	return u
}

// sameUsage compares costs with a tolerance because SQLite sums in its own
// order, which can differ from the order used to compute the expectation.
func sameUsage(want, got statev2.Usage) bool {
	tolerance := 1e-9 * math.Max(1, math.Abs(want.Cost))
	return want.Messages == got.Messages &&
		math.Abs(want.Cost-got.Cost) <= tolerance &&
		want.TokensInput == got.TokensInput && want.TokensOutput == got.TokensOutput &&
		want.TokensReasoning == got.TokensReasoning &&
		want.TokensCacheRead == got.TokensCacheRead && want.TokensCacheWrite == got.TokensCacheWrite
}
