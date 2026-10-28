package remote

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (p *remotePlatform) BackgroundTasks(ctx context.Context, id string) (*platforms.BackgroundSnapshot, error) {
	if !p.Capabilities().BackgroundTaskLifecycle {
		return nil, platforms.ErrUnsupported
	}
	return jsonCall(ctx, p, "BackgroundTasks", id, nil, &platforms.BackgroundSnapshot{})
}

func (p *remotePlatform) SetBackgroundLifecycle(ctx context.Context, req platforms.SetBackgroundLifecycleRequest) error {
	if !p.Capabilities().BackgroundTaskLifecycle {
		return platforms.ErrUnsupported
	}
	return p.mutate(ctx, "SetBackgroundLifecycle", req)
}

func (s *Server) BackgroundTasks(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	manager, ok := p.(platforms.BackgroundTaskManager)
	if !ok || !p.Capabilities().BackgroundTaskLifecycle {
		return nil, svcErr(platforms.ErrUnsupported)
	}
	return jsonResp(manager.BackgroundTasks(ctx, req.SessionId))
}

func (s *Server) SetBackgroundLifecycle(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var update platforms.SetBackgroundLifecycleRequest
	if err := s.decodeSessionRequest(req, &update); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.SetBackgroundLifecycle(ctx, req.Platform, update))
}
