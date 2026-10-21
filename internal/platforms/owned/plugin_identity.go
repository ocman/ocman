package owned

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessioncatalog"
)

func (a *Adapter) PluginSessionReference(ctx context.Context, id string) (string, error) {
	if !sessioncatalog.IsID(id) {
		return "", sessioncatalog.ErrInvalid
	}
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if row.OwnerID != a.owner || row.Platform != string(a.ID()) {
		return "", platforms.ErrHistoryUnavailable
	}
	if row.LegacyKeys {
		if row.NativeID == "" {
			return "", sessioncatalog.ErrCreating
		}
		return row.NativeID, nil
	}
	return row.ID, nil
}
