package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func nativePlatformKind(p platforms.Platform) string {
	if named, ok := p.(interface{ NativeKind() platforms.ID }); ok {
		return string(named.NativeKind())
	}
	_, kind := platforms.SplitPlatformID(string(p.ID()))
	return kind
}

func registryPlatformKey(registry IdentityRegistry, p platforms.Platform) string {
	if named, ok := registry.(interface {
		KeyFor(platforms.Platform) (platforms.ID, bool)
	}); ok {
		if key, found := named.KeyFor(p); found {
			return string(key)
		}
	}
	return string(p.ID())
}

// StoredSessionIdentity proves this exact canonical source without requiring a
// live credential store. It is a read-only identity, never execution authority.
func (a *Adapter) StoredSessionIdentity(ctx context.Context, id string) (*platforms.SessionIdentity, error) {
	row, err := a.storedIdentity(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.sourceScoped && row.SourceKey != a.source.SourceKey {
		return nil, platforms.ErrHistoryUnavailable
	}
	info := identityInfo(row)
	info.NativeID, info.NativeParentID = "", ""
	return info, nil
}
