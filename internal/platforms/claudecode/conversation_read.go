package claudecode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (p *claudePlatform) ConversationHead(ctx context.Context, id string) (*platforms.ConversationHead, error) {
	if p.history == nil {
		return nil, fmt.Errorf("claude history metadata unavailable")
	}
	row, version, model, err := p.history.Head(ctx, id)
	h := &platforms.ConversationHead{Session: row, Version: version, DefaultModel: model}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if owner := p.projects.owner(id); err == nil && version == "" && owner != nil && owner.Available(ctx) {
		h.LiveOnly = true
	}
	if !h.LiveOnly {
		identity, identityErr := p.HistoryIdentity(ctx)
		if identityErr != nil {
			return nil, identityErr
		}
		h.Identity = identity
		if errors.Is(err, sql.ErrNoRows) {
			return h, platforms.ErrNotFound
		}
	}
	row.Platform = string(p.ID())
	if live := p.LiveStatus(id); live != nil {
		row.PendingPermission, row.PendingQuestion = live.PendingPermission, live.PendingQuestion
		if live.Status != "" {
			row.Status = db.SessionStatus(live.Status)
		}
	}
	h.Session, h.Version, h.DefaultModel = row, version, model
	return h, nil
}
