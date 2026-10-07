package remote

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *remoteHost) ReloadOpencode(ctx context.Context) error {
	client := h.conn.Client()
	if client == nil {
		return ErrRemoteOffline
	}
	_, err := client.ReloadOpencode(ctx, &pb.Empty{})
	if status.Code(err) == codes.FailedPrecondition {
		var rejection platforms.UpstreamError
		if json.Unmarshal([]byte(status.Convert(err).Message()), &rejection) == nil {
			return &rejection
		}
	}
	return remotePlatformError(err)
}

func (s *Server) ReloadOpencode(ctx context.Context, _ *pb.Empty) (*pb.Empty, error) {
	err := s.host.ReloadOpencode(ctx)
	var rejection *platforms.UpstreamError
	if errors.As(err, &rejection) {
		payload, _ := json.Marshal(rejection)
		return nil, status.Error(codes.FailedPrecondition, string(payload))
	}
	return &pb.Empty{}, svcErr(err)
}
