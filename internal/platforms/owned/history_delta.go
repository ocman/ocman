package owned

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) HistoryDelta(ctx context.Context, id, cursor string) (*db.HistorySnapshot, error) {
	reader, ok := a.native.(platforms.HistoryDeltaReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	snapshot, err := reader.HistoryDelta(ctx, nativeID, cursor)
	if err != nil {
		return nil, err
	}
	return a.historySnapshot(ctx, nativeID, snapshot)
}
