package remote

import (
	"context"
	"errors"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platformconnections"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *Server) PlatformConnections(ctx context.Context, req *pb.CallReq) (response *pb.JsonResp, err error) {
	defer func() {
		switch {
		case errors.Is(err, platformconnections.ErrInvalid):
			err = status.Error(codes.InvalidArgument, "invalid connection request")
		case errors.Is(err, platformconnections.ErrNotFound):
			err = status.Error(codes.NotFound, "connection not found")
		case errors.Is(err, platformconnections.ErrUnavailable), errors.Is(err, platformconnections.ErrLimit):
			err = status.Error(codes.FailedPrecondition, "connection unavailable")
		}
	}()
	h, ok := s.host.(hostsvc.ConnectionHost)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "platform connections unsupported")
	}
	var op connectionOperation
	if err := unmarshalJSON(req.Payload, &op); err != nil {
		return nil, err
	}
	switch op.Action {
	case "list":
		return jsonResp(h.InventoryPlatformConnections(ctx))
	case "check":
		check, err := h.CheckPlatformConnection(ctx, op.Check)
		if err != nil && check.ReservationID == "" {
			return nil, err
		}
		return jsonResp(connectionCheckResult{Check: check, Failed: err != nil}, nil)
	case "prepare":
		return jsonResp(h.PreparePlatformConnection(ctx, op.Setup))
	case "save":
		return jsonResp(h.SavePlatformConnection(ctx, op.ID, op.Name, op.Enabled))
	case "cancel":
		return &pb.JsonResp{}, h.CancelPlatformConnectionSetup(ctx, op.ID)
	case "update":
		return &pb.JsonResp{}, h.UpdatePlatformConnection(ctx, op.Update)
	case "remove":
		return &pb.JsonResp{}, h.RemovePlatformConnection(ctx, op.ID)
	case "onboarding":
		return &pb.JsonResp{}, h.CompletePlatformConnectionOnboarding(ctx)
	default:
		return nil, status.Error(codes.InvalidArgument, "unknown platform connection action")
	}
}
