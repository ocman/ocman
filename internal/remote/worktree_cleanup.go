package remote

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (h *remoteHost) CleanupWorktree(ctx context.Context, req hostsvc.CleanupWorktreeRequest) error {
	err := hostExec(ctx, h.conn, "CleanupWorktree", req)
	if status.Code(err) == codes.Unimplemented {
		return platforms.ErrUnsupported
	}
	return err
}

func (h *remoteHost) RestoreWorktree(ctx context.Context, checkpoint hostsvc.WorktreeCheckpoint) error {
	return hostExec(ctx, h.conn, "RestoreWorktree", checkpoint)
}

func (s *Server) CleanupWorktree(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var cleanup hostsvc.CleanupWorktreeRequest
	if err := unmarshalJSON(req.Payload, &cleanup); err != nil {
		return nil, err
	}
	host, ok := s.host.(hostsvc.WorktreeMaintenance)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "owner does not support guarded worktree cleanup")
	}
	return &pb.JsonResp{}, host.CleanupWorktree(ctx, cleanup)
}

func (s *Server) RestoreWorktree(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var checkpoint hostsvc.WorktreeCheckpoint
	if err := unmarshalJSON(req.Payload, &checkpoint); err != nil {
		return nil, err
	}
	host, ok := s.host.(hostsvc.WorktreeMaintenance)
	if !ok {
		return nil, platforms.ErrUnsupported
	}
	return &pb.JsonResp{}, host.RestoreWorktree(ctx, checkpoint)
}
