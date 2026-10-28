package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) storedInactive(ctx context.Context, cutoff int64) ([]db.SessionArchiveCandidate, error) {
	instances, err := a.storedReadInstances(ctx, "")
	if err != nil {
		return nil, err
	}
	out := []db.SessionArchiveCandidate{}
	for _, instance := range instances {
		rows, err := a.history.Sessions(ctx, instance.ID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row.SourceDeleted || row.Subagent || row.UpdatedAt >= cutoff {
				continue
			}
			if _, err := a.storedIdentity(ctx, row.SessionID); errors.Is(err, platforms.ErrNotFound) || errors.Is(err, sessioncatalog.ErrCreating) {
				continue
			} else if err != nil {
				return nil, err
			}
			out = append(out, db.SessionArchiveCandidate{ID: row.SessionID, TimeUpdated: row.UpdatedAt})
		}
	}
	return out, nil
}
