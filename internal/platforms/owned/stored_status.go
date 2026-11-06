package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

// Event status needs identity, terminal inference and a port-fenced live turn,
// not totals, models, background snapshots or the platform's entire parent map.
func (a *Adapter) storedStatus(ctx context.Context, id, port string) (db.SessionStatus, error) {
	identity, err := a.storedIdentity(ctx, id)
	if err != nil {
		return "", err
	}
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return "", err
	}
	summary, err := a.history.SessionStatusSummary(ctx, key)
	if errors.Is(err, statev2.ErrNotFound) {
		err = platforms.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	inferred := conversationread.InferredStatus(summary)
	if identity.SourceKey != a.source.SourceKey || a.checkSource(ctx) != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return db.SettleSessionStatus(db.TurnUnobserved, false, inferred), nil
	}
	if settler, ok := a.native.(platforms.StoredSessionStatusSettler); ok {
		return settler.SettleStoredSessionStatus(identity.NativeID, port, inferred), nil
	}
	row := db.Session{ID: identity.NativeID, Directory: summary.Directory, Status: inferred}
	a.enrichObservedStatus(&row)
	return row.Status, nil
}
