package owned

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) NativePermissionState(ctx context.Context, id string) (*platforms.NativePermissionState, error) {
	native, ok := a.native.(platforms.NativePermissions)
	if !ok || !a.Capabilities().NativeAutoPermission {
		return nil, platforms.ErrUnsupported
	}
	id, err := a.nativeID(ctx, id)
	if err != nil {
		return nil, err
	}
	return native.NativePermissionState(ctx, id)
}
