package owned

import (
	"context"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func (a *Adapter) PermissionInboxKey(ctx context.Context, scope, id, permission string) (string, error) {
	remote, base := platforms.SplitPlatformID(scope)
	if (remote != "" && remote != a.owner) || base != string(a.ID()) || permission == "" {
		return "", sessioncatalog.ErrInvalid
	}
	if _, err := a.nativeID(ctx, id); err != nil {
		return "", err
	}
	row, err := a.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	if row.Purged {
		return "", platforms.ErrNotFound
	}
	seed := row.ID
	if row.LegacyKeys {
		seed = row.NativeID
	}
	return "permission-" + uuid.NewSHA1(uuid.NameSpaceOID, []byte(scope+"\x00"+seed+"\x00"+permission)).String(), nil
}
