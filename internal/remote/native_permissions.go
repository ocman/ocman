package remote

import (
	"context"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

func (p *remotePlatform) NativePermissionState(ctx context.Context, id string) (*platforms.NativePermissionState, error) {
	if !p.Capabilities().NativeAutoPermission {
		return nil, platforms.ErrUnsupported
	}
	payload, err := p.call(ctx, "NativePermissionState", id, nil)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var state platforms.NativePermissionState
	return canonicalDecodeResult(p, payload, &state)
}

func (s *Server) NativePermissionState(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	native, ok := p.(platforms.NativePermissions)
	if !ok || !p.Capabilities().NativeAutoPermission {
		return nil, svcErr(platforms.ErrUnsupported)
	}
	state, err := native.NativePermissionState(ctx, req.SessionId)
	return jsonResp(state, svcErr(err))
}
