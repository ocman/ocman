package claudecode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

// SessionIdentityMetadata reads indexed transcript metadata, never content,
// proving the session belongs to this configuration before it is bound.
func (p *claudePlatform) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	if p.history == nil {
		return nil, fmt.Errorf("%w: claude history metadata unavailable", platforms.ErrHistoryUnavailable)
	}
	row, _, _, err := p.history.Head(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		if p.projects != nil {
			return p.projects.runtimeSession(ctx, id)
		}
		return nil, platforms.ErrNotFound
	}
	return row, err
}
