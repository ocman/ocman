package claudecode

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// SessionIdentityMetadata reads indexed transcript metadata, never content,
// proving the session belongs to this configuration before it is bound.
func (p *claudePlatform) SessionIdentityMetadata(ctx context.Context, id string) (*db.Session, error) {
	if p.history == nil {
		return nil, fmt.Errorf("%w: claude history metadata unavailable", platforms.ErrHistoryUnavailable)
	}
	row, _, _, err := p.history.Head(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platforms.ErrNotFound
	}
	return row, err
}
