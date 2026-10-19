package opencode

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) HistoryIdentity(ctx context.Context) (platforms.HistoryIdentity, error) {
	if a.db == nil {
		return platforms.HistoryIdentity{}, fmt.Errorf("opencode history source unavailable")
	}
	key, err := a.db.HistorySourceKey(ctx)
	return platforms.HistoryIdentity{Platform: string(PlatformID), SourceKey: key, Name: "OpenCode"}, err
}

func (a *Adapter) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	if a.db == nil {
		return nil, fmt.Errorf("opencode history source unavailable")
	}
	return a.db.HistoryInventory(ctx)
}

func (a *Adapter) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	if a.db == nil {
		return nil, fmt.Errorf("opencode history source unavailable")
	}
	return a.db.HistorySnapshot(ctx, id)
}
