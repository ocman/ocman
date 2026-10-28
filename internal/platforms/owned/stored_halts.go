package owned

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) bindStoredHaltIdentity() {
	if source, ok := a.native.(platforms.SessionHaltIdentity); ok {
		source.SetSessionHaltIdentity(func(ctx context.Context, native string) (string, error) {
			row, err := a.historyIdentity(ctx, native)
			if err == nil && row.Purged {
				return "", platforms.ErrNotFound
			}
			// The one-time authenticated cutover froze these sessions' native
			// receipt keys. Preserve their first observation across replays;
			// every identity discovered after cutover uses canonical keys.
			if err == nil && row.LegacyKeys {
				return row.NativeID, nil
			}
			return row.ID, err
		}, func(ctx context.Context, id string) (string, error) {
			if !sessioncatalog.IsID(id) {
				row, err := a.store.FindBinding(ctx, a.binding(id))
				if errors.Is(err, sessioncatalog.ErrNotFound) {
					return "", nil
				}
				if err != nil {
					return "", err
				}
				if !row.LegacyKeys || row.Purged || row.CreationState != sessioncatalog.Ready {
					return "", nil
				}
				return row.NativeID, nil
			}
			row, err := a.storedIdentity(ctx, id)
			if errors.Is(err, platforms.ErrNotFound) || errors.Is(err, platforms.ErrHistoryUnavailable) || errors.Is(err, sessioncatalog.ErrInvalid) || errors.Is(err, sessioncatalog.ErrCreating) {
				return "", nil
			}
			if err != nil {
				return "", err
			}
			if row.SourceKey != a.source.SourceKey {
				return "", nil
			}
			return row.NativeID, nil
		})
	}
}
