package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) GetSessionIdentities(ctx context.Context) ([]db.Session, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	return a.db.GetSessionIdentities(ctx)
}

func (a *Adapter) GetSessionLifecycle(ctx context.Context, id string) (db.SessionLifecycle, error) {
	if a.db == nil {
		return db.SessionLifecycle{}, platforms.ErrNotFound
	}
	return a.db.GetSessionLifecycle(ctx, id)
}
