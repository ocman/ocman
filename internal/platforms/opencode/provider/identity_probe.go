package opencode

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// SessionIdentityMetadata reads the indexed session row only, proving the
// session belongs to this OpenCode database before it is bound.
func (a *Adapter) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	row, err := a.db.GetSession(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platforms.ErrNotFound
	}
	return row, err
}
