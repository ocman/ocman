package conversationingest

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func inventoryDeferrals(err error) (int, error) {
	var deferred *platforms.HistoryDeferred
	if errors.As(err, &deferred) && deferred.Count > 0 {
		return deferred.Count, nil
	}
	return 0, err
}

func readInventory(ctx context.Context, reader platforms.HistoryReader) ([]db.HistoryEntry, int, error) {
	rows, err := reader.HistoryInventory(ctx)
	deferred, err := inventoryDeferrals(err)
	return rows, deferred, err
}
