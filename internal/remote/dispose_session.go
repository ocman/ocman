package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

var _ platforms.SessionDisposer = (*remotePlatform)(nil)

func (p *remotePlatform) DisposeSession(ctx context.Context, req platforms.DisposeSessionRequest) error {
	return p.mutate(ctx, req, func(c pb.OcmanClient, b []byte) error {
		_, err := c.DisposeSession(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: b})
		return err
	})
}

func (s *Server) DisposeSession(ctx context.Context, req *pb.PlatformJsonReq) (*pb.Empty, error) {
	var dispose platforms.DisposeSessionRequest
	if err := unmarshalJSON(req.Payload, &dispose); err != nil {
		return nil, err
	}
	return &pb.Empty{}, svcErr(s.sessions.Dispose(ctx, req.Platform, dispose))
}
