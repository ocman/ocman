package provider

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) NativePermissionState(ctx context.Context, id string) (*platforms.NativePermissionState, error) {
	var state platforms.NativePermissionState
	err := a.call(ctx, a.Capabilities().NativeAutoPermission, "GET", sessionPath(id, "/native-permission"), nil, &state)
	return &state, err
}
