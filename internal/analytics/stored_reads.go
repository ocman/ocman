package analytics

import (
	"context"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
)

var errHistoryImportIncomplete = errors.New("statev2 analytics unavailable: import-incomplete")

// withStoredAnalytics fixes one validated statev2 read snapshot for a complete
// background operation, just as analyticsRoute does for an HTTP response.
func (m *Module) withStoredAnalytics(ctx context.Context, read func(*db.DB) error) error {
	var history *HistoryAnalytics
	if m.deps.HistoryAnalytics != nil {
		history = m.deps.HistoryAnalytics()
	}
	reader, release, reason := history.projection(ctx)
	if reader == nil {
		if reason == "import-incomplete" {
			return errHistoryImportIncomplete
		}
		return fmt.Errorf("statev2 analytics unavailable: %s", reason)
	}
	defer release()
	return read(reader)
}
