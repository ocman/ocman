package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// SessionMetadata is intentionally narrower than SessionSummary: titles need
// only the indexed session row, not its message/part aggregates or live state.
func (a *Adapter) SessionMetadata(ctx context.Context, id string) (*db.Session, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	return a.db.GetSession(ctx, id)
}
