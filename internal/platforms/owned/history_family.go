package owned

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) HistoryFamilyInventory(ctx context.Context, id string) ([]db.HistoryEntry, error) {
	reader, ok := a.native.(platforms.HistoryFamilyReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	nativeID, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := reader.HistoryFamilyInventory(ctx, nativeID)
	return a.historyEntries(ctx, rows, err)
}
