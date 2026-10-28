package remote

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

var _ platforms.SessionDisposer = (*remotePlatform)(nil)

func (p *remotePlatform) DisposeSession(ctx context.Context, req platforms.DisposeSessionRequest) error {
	return p.mutate(ctx, "DisposeSession", req)
}

func (s *Server) DisposeSession(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var dispose platforms.DisposeSessionRequest
	if err := s.decodeSessionRequest(req, &dispose); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, svcErr(s.sessions.Dispose(ctx, req.Platform, dispose))
}
