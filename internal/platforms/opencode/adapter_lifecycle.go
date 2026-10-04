package opencode

import (
	"context"
	"database/sql"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// SessionLifecycle implements platforms.LifecycleReader: one bounded
// latest-message query settled against the live turn state, the same rule
// Session applies, without loading messages, parts, the session tree or
// costs.
func (a *Adapter) SessionLifecycle(ctx context.Context, sessionID string) (*platforms.SessionLifecycle, error) {
	if a.db == nil {
		return nil, platforms.ErrNotFound
	}
	l, err := a.db.GetSessionLifecycle(ctx, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, platforms.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &platforms.SessionLifecycle{
		Status:               a.settleStatus(sessionID, l.Directory, l.Status, discoverOpenCodePorts()),
		LatestMessageID:      l.LatestMessageID,
		LatestMessageCreated: l.LatestMessageCreated,
		LatestMessageRole:    l.LatestMessageRole,
	}, nil
}
