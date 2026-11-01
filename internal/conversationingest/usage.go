package conversationingest

import (
	"github.com/ocman/ocman/internal/statev2"
)

// expectedUsage totals a normalized batch the way Usage reads it back, so the
// import commit can detect a column that did not receive its value. A
// mismatch means a column mapping or normalization bug.
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
