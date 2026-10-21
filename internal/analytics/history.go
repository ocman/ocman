package analytics

import (
	"time"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// HistoryAnalytics configures the conversation analytics reader. Instances must
// describe initialized, configured local sources. The bool reports completeness;
// an incomplete import still serves committed data without a native fallback.
type HistoryAnalytics struct {
	Store       *statev2.DB
	Instances   func() ([]string, bool)
	CheckedAt   func() time.Time
	projections projectionReaders
	// validated runs between scope validation and the response; tests use it
	// to commit concurrent ingestion at the race window.
	validated func()
}
