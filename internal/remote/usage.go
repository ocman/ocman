package remote

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var _ platforms.UsageReader = (*remotePlatform)(nil)

func (p *remotePlatform) SessionUsage(ctx context.Context, id string) (map[string]platforms.Usage, error) {
	payload, err := p.call(ctx, "SessionUsage", id, nil)
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var usage map[string]platforms.Usage
	if err := unmarshalJSON(payload, &usage); err != nil {
		return nil, err
	}
	return usage, nil
}

func (s *Server) SessionUsage(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session id is required")
	}
	adapter, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	reader, ok := adapter.(platforms.UsageReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "session usage is unsupported")
	}
	usage, err := reader.SessionUsage(ctx, req.SessionId)
	return jsonResp(usage, svcErr(err))
}
