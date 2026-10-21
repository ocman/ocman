package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) refreshStored(ctx context.Context, id string) error {
	if _, err := a.storedIdentity(ctx, id); err != nil {
		return err
	}
	if a.historyRefresh == nil {
		return platforms.ErrHistoryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, platforms.StoredHistoryWait)
	defer cancel()
	return a.historyRefresh(ctx, id)
}
