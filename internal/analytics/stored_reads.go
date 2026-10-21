package analytics

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
)

// withStoredAnalytics fixes one validated statev2 read snapshot for a complete
// background operation, just as analyticsRoute does for an HTTP response.
func (m *Module) withStoredAnalytics(ctx context.Context, read func(*db.DB) error) error {
	var history *HistoryAnalytics
	if m.deps.HistoryAnalytics != nil {
		history = m.deps.HistoryAnalytics()
	}
	reader, release, reason := history.projection(ctx)
	if reader == nil {
		return fmt.Errorf("statev2 analytics unavailable: %s", reason)
	}
	defer release()
	return read(reader)
}
