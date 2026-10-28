package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

// Batch validation is deliberately identical to storedIdentity. No persistent
// identity cache may hide a purge, publication change or source replacement.
func (a *Adapter) storedIdentities(ctx context.Context, ids []string) (map[string]sessioncatalog.Session, error) {
	for _, id := range ids {
		if !sessioncatalog.IsID(id) {
			return nil, sessioncatalog.ErrInvalid
		}
	}
	rows, err := a.store.GetMany(ctx, ids)
	if err != nil {
		return nil, err
	}
	for id, row := range rows {
		_, err := a.validateStoredIdentity(id, row)
		if errors.Is(err, platforms.ErrNotFound) || errors.Is(err, sessioncatalog.ErrCreating) {
			delete(rows, id)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return rows, nil
}
