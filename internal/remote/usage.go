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
	p.conn.mu.RLock()
	conn := p.conn.conn
	p.conn.mu.RUnlock()
	if conn == nil {
		return nil, ErrRemoteOffline
	}
	resp, err := pb.NewUsageClient(conn).SessionUsage(ctx, &pb.SessionRef{Platform: p.base, SessionId: id})
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var usage map[string]platforms.Usage
	if err := unmarshalJSON(resp.Payload, &usage); err != nil {
		return nil, err
	}
	return usage, nil
}

type usageServer struct {
	pb.UnimplementedUsageServer
	owner *Server
}

func (s *usageServer) SessionUsage(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session id is required")
	}
	adapter, err := s.owner.platformFor(req.Platform)
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
