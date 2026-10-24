package claudecode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (p *claudePlatform) HistoryIdentity(context.Context) (platforms.HistoryIdentity, error) {
	if p.history == nil {
		return platforms.HistoryIdentity{}, fmt.Errorf("claude history source unavailable")
	}
	key, err := canonicalHistoryConfig(p.configDir)
	if err != nil {
		return platforms.HistoryIdentity{}, err
	}
	return platforms.HistoryIdentity{Platform: "claudecode", SourceKey: key, Name: "Claude Code"}, nil
}

func (p *claudePlatform) HistoryInventory(ctx context.Context) ([]db.HistoryEntry, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history source unavailable")
	}
	rows, err := p.history.Inventory(ctx)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, row := range rows {
		seen[row.ID] = true
	}
	if p.projects == nil {
		return rows, nil
	}
	live, err := p.projects.historyRuntimeInventory(ctx)
	var incomplete *platforms.HistoryDeferred
	if err != nil && !errors.As(err, &incomplete) {
		return nil, err
	}
	for _, row := range live {
		if !seen[row.ID] {
			rows = append(rows, db.HistoryEntry{ID: row.ID, Version: "runtime:" + row.ID})
			seen[row.ID] = true
		}
	}
	return rows, err
}

func (p *claudePlatform) HistorySnapshot(ctx context.Context, id string) (*db.HistorySnapshot, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history source unavailable")
	}
	snapshot, err := p.history.Snapshot(ctx, id)
	if !errors.Is(err, sql.ErrNoRows) && !errors.Is(err, os.ErrNotExist) {
		return snapshot, err
	}
	if p.projects == nil {
		return nil, err
	}
	if _, err := p.projects.runtimeSession(ctx, id); err != nil {
		return nil, err
	}
	adapter, _, routeErr := p.projects.route(ctx, id, false)
	if routeErr != nil {
		return nil, routeErr
	}
	if adapter != nil {
		detail, err := adapter.RuntimeDetail(ctx, id, math.MaxInt, 0)
		if err != nil {
			return nil, err
		}
		if detail == nil || detail.Session == nil || detail.Session.ID != id {
			return nil, platforms.ErrHistoryUnavailable
		}
		return &db.HistorySnapshot{Session: *detail.Session, Subagent: detail.Session.ParentID != "", Partial: true, Version: "runtime:" + id, Messages: detail.Messages, Parts: detail.Parts}, nil
	}
	return nil, err
}

func (p *claudePlatform) HistoryDelta(ctx context.Context, id, cursor string) (*db.HistorySnapshot, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history source unavailable")
	}
	snapshot, _, err := p.history.Delta(ctx, id, cursor)
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
		return p.HistorySnapshot(ctx, id)
	}
	return snapshot, err
}
