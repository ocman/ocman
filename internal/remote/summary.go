package remote

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *remotePlatform) SessionSummary(ctx context.Context, id string) (*db.Session, error) {
	row, err := jsonCall(ctx, p, func(c pb.OcmanClient) (*pb.JsonResp, error) {
		return c.SessionSummary(ctx, &pb.SessionRef{Platform: p.base, SessionId: id})
	}, &db.Session{})
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.Join(platforms.ErrUnsupported, err)
	}
	if err != nil {
		return nil, err
	}
	row.Platform = string(p.ID())
	row.RemoteID = p.remoteID()
	row.RemoteName = p.nameFn()
	return row, nil
}

func (s *Server) SessionSummary(ctx context.Context, req *pb.SessionRef) (*pb.JsonResp, error) {
	p, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	reader, ok := p.(platforms.SummaryReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform has no summary read")
	}
	return jsonResp(reader.SessionSummary(ctx, req.SessionId))
}
