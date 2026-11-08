package remote

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Resources reads the owner's managed process tree. Session links are added
// by the hub against its own catalogue, so the owner's raw sample is enough.
func (h *remoteHost) Resources(ctx context.Context) (hostsvc.ResourceSnapshot, error) {
	snapshot, err := hostCall[hostsvc.ResourceSnapshot](ctx, h.conn, "Resources", nil)
	if status.Code(err) == codes.Unimplemented {
		return hostsvc.ResourceSnapshot{}, platforms.ErrUnsupported
	}
	return snapshot, err
}

func (s *Server) Resources(ctx context.Context, _ *pb.CallReq) (*pb.JsonResp, error) {
	viewer, ok := s.host.(hostsvc.ResourceViewer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "owner does not support resource inspection")
	}
	return jsonResp(viewer.Resources(ctx))
}
