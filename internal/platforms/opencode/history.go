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
	snapshot, err := a.db.HistorySnapshot(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("reading OpenCode source snapshot: %w", err)
	}
	return a.historyProject(ctx, snapshot, err)
}

func (a *Adapter) historyProject(ctx context.Context, snapshot *db.HistorySnapshot, err error) (*db.HistorySnapshot, error) {
	if err != nil || snapshot == nil {
		return snapshot, err
	}
	if snapshot.Session.ProjectID != "" {
		snapshot.ProjectDirectory, err = a.db.ProjectDirectory(ctx, snapshot.Session.ProjectID)
		if err != nil {
			return nil, fmt.Errorf("reading OpenCode project evidence: %w", err)
		}
	}
	return snapshot, err
}
