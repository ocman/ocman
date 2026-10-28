package owned

import (
	"context"

	"github.com/ocman/ocman/internal/conversationread"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) storedMetadata(ctx context.Context, id string) (*db.Session, error) {
	identity, err := a.storedIdentity(ctx, id)
	if err != nil {
		return nil, err
	}
	key, err := a.storedKey(ctx, id)
	if err != nil {
		return nil, err
	}
	s, err := a.history.SessionState(ctx, key)
	if err != nil {
		return nil, err
	}
	if s.Revision == 0 {
		return nil, platforms.ErrHistoryUnavailable
	}
	row := conversationread.Metadata(s, string(a.ID()))
	if identity.Origin == sessioncatalog.Routine {
		row.RoutineID = identity.OriginID
	}
	if identity.Origin == sessioncatalog.Factory {
		row.FactoryAttemptID = identity.OriginID
	}
	return &row, nil
}

// CachedSession is a cheap event hint: one indexed statev2 row, never a
// transcript read or a native call.
func (a *Adapter) CachedSession(id string) (db.Session, bool) {
	row, err := a.storedMetadata(context.Background(), id)
	if err != nil || row == nil {
		return db.Session{}, false
	}
	return *row, true
}
