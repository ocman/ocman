package claudecode

import (
	"context"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func (p *claudePlatform) HistoryFamilyInventory(ctx context.Context, id string) ([]db.HistoryEntry, error) {
	if p.history == nil {
		return nil, platforms.ErrHistoryUnavailable
	}
	return p.history.Family(ctx, id)
}
