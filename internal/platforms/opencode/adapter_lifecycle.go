package opencode

import (
	"context"
	"database/sql"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// SessionLifecycle implements platforms.LifecycleReader: one bounded
// latest-message read settled against the live turn state. Unclosed assistant
// messages check scalar terminal-part metadata to recognize completed shells;
// messages, parts, the session tree and costs are not loaded.
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
