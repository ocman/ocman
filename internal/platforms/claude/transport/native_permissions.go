package claudecode

import (
	"context"
	"github.com/ocman/ocman/internal/platforms"
)

func (p *claudePlatform) NativePermissionState(ctx context.Context, id string) (*platforms.NativePermissionState, error) {
	a, err := p.adapter(ctx, id, false)
	if err != nil {
		return nil, err
	}
	return a.NativePermissionState(ctx, id)
}

func (p *claudePlatform) SetPermissionRules(ctx context.Context, req platforms.SetPermissionRulesRequest) error {
	if req.NativeMode == nil {
		return p.Adapter.SetPermissionRules(ctx, req)
	}
	a, err := p.adapter(ctx, req.SessionID, true)
	if err != nil {
		return err
	}
	return a.SetPermissionRules(ctx, req)
}
