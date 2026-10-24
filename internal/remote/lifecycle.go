package remote

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
)

// SessionLifecycle implements platforms.LifecycleReader over the
// SessionLifecycle RPC. An owner predating the RPC answers Unimplemented,
// reported as ErrUnsupported so the caller falls back to Session.
func (p *remotePlatform) SessionLifecycle(ctx context.Context, sessionID string) (*platforms.SessionLifecycle, error) {
	l, err := jsonCall(ctx, p, "SessionLifecycle", sessionID, nil, &platforms.SessionLifecycle{})
	if status.Code(err) == codes.Unimplemented {
		return nil, errors.Join(platforms.ErrUnsupported, err)
	}
	return l, err
}

// SessionLifecycle serves the owner's bounded lifecycle read.
func (s *Server) SessionLifecycle(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	p, err := s.sessionPlatformFor(req.Platform, req.SessionId)
	if err != nil {
		return nil, err
	}
	reader, ok := p.(platforms.LifecycleReader)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform has no bounded lifecycle read")
	}
	return jsonResp(reader.SessionLifecycle(ctx, req.SessionId))
}
