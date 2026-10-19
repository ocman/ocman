package claudecode

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (p *claudePlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	if p.history == nil {
		return platforms.HistoryIdentity{}, fmt.Errorf("claude history source unavailable")
	}
	key, err := filepath.EvalSymlinks(p.configDir)
	if err != nil {
		return platforms.HistoryIdentity{}, err
	}
	key, err = filepath.Abs(key)
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: key, Name: "Claude Code"}, err
}

func (p *claudePlatform) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history source unavailable")
	}
	return p.history.Inventory(ctx)
}

func (p *claudePlatform) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history source unavailable")
	}
	return p.history.Snapshot(ctx, id)
}
