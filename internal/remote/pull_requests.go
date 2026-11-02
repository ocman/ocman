package remote

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PullRequestHandler func(context.Context, string, platforms.PullRequestRequest) platforms.PullRequestResponse

func (s *Server) UsePullRequests(handler PullRequestHandler) *Server {
	s.pullRequests = handler
	return s
}

func (s *Server) PullRequestOperation(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if owner, _ := platforms.SplitPlatformID(req.Platform); owner != "" {
		return nil, status.Error(codes.InvalidArgument, "PR operations require an owner-local platform")
	}
	if _, err := s.platformFor(req.Platform); err != nil {
		return nil, err
	}
	if s.pullRequests == nil {
		return nil, status.Error(codes.Unavailable, "PR workflow unavailable")
	}
	var operation platforms.PullRequestRequest
	if len(req.Payload) > 1<<20 || unmarshalJSON(req.Payload, &operation) != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid PR operation")
	}
	return jsonResp(s.pullRequests(ctx, req.Platform, operation), nil)
}

func (p *remotePlatform) PullRequestOperation(ctx context.Context, req platforms.PullRequestRequest) (platforms.PullRequestResponse, error) {
	response, err := jsonCall(ctx, p, "PullRequestOperation", req.SessionID, req, &platforms.PullRequestResponse{})
	if err != nil {
		return platforms.PullRequestResponse{}, err
	}
	return *response, nil
}
