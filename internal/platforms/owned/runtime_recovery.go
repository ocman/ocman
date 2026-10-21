package owned

import (
	"context"
	"database/sql"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (a *Adapter) GetSessionIdentities(ctx context.Context) ([]db.Session, error) {
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	reader, ok := a.native.(platforms.RuntimeSessionReader)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	rows, err := reader.GetSessionIdentities(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]db.Session, 0, len(rows))
	for _, row := range rows {
		bound, err := a.store.FindBinding(ctx, a.binding(row.ID))
		if errors.Is(err, sessioncatalog.ErrNotFound) {
			if err := a.importRows(ctx, []db.Session{row}); err != nil {
				return nil, err
			}
			bound, err = a.store.FindBinding(ctx, a.binding(row.ID))
			if errors.Is(err, sessioncatalog.ErrNotFound) {
				return nil, sessioncatalog.ErrCreating
			}
		}
		if err != nil {
			return nil, err
		}
		if bound.Purged || bound.CreationState == sessioncatalog.CreationFailed {
			continue
		}
		row.ID, row.ParentID, row.Platform = bound.ID, bound.ParentID, string(a.ID())
		out = append(out, row)
	}
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// This is persisted evidence, not runtime execution. It remains readable during
// creation configuration, but cannot cross owners or reactivate purged sessions.
func (a *Adapter) GetSessionLifecycle(ctx context.Context, id string) (db.SessionLifecycle, error) {
	if !sessioncatalog.IsID(id) {
		return db.SessionLifecycle{}, sessioncatalog.ErrInvalid
	}
	if err := a.checkSource(ctx); err != nil {
		return db.SessionLifecycle{}, err
	}
	bound, err := a.store.Get(ctx, id)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	if bound.OwnerID != a.owner || bound.Platform != string(a.ID()) || bound.SourceKey != a.source.SourceKey {
		return db.SessionLifecycle{}, platforms.ErrHistoryUnavailable
	}
	if bound.Purged {
		return db.SessionLifecycle{}, sql.ErrNoRows
	}
	if bound.NativeID == "" {
		return db.SessionLifecycle{}, sessioncatalog.ErrCreating
	}
	reader, ok := a.native.(platforms.RuntimeSessionReader)
	if !ok {
		return db.SessionLifecycle{}, platforms.ErrUnsupported
	}
	lifecycle, err := reader.GetSessionLifecycle(ctx, bound.NativeID)
	if err != nil {
		return db.SessionLifecycle{}, err
	}
	if err := a.checkSource(ctx); err != nil {
		return db.SessionLifecycle{}, err
	}
	return lifecycle, nil
}
