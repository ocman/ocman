package remote

import (
	"context"

	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (h *remoteHost) CreateOneOffDirectory(ctx context.Context) (string, error) {
	return hostCall[string](ctx, h.conn, "CreateOneOffDirectory", struct{}{})
}

func (s *Server) CreateOneOffDirectory(ctx context.Context, _ *pb.CallReq) (*pb.JsonResp, error) {
	return jsonResp(s.host.CreateOneOffDirectory(ctx))
}
