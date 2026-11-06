package remote

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *remotePlatform) SessionMetadata(ctx context.Context, id string) (*db.Session, error) {
	row, err := jsonCall(ctx, p, "SessionMetadata", id, nil, &db.Session{})
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.Join(platforms.ErrUnsupported, err)
	}
	if err != nil {
		return nil, err
	}
	row.Platform, row.RemoteID, row.RemoteName = string(p.ID()), p.remoteID(), p.nameFn()
	return p.checkedSummary(row, id)
}

func (s *Server) SessionMetadata(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	reader, ok := p.(platforms.SessionMetadataReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform has no metadata read")
	}
	return jsonResp(reader.SessionMetadata(ctx, req.SessionId))
}
