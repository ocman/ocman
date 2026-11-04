package remote

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	pb "github.com/ocman/ocman/internal/remote/proto"
)

func (h *remoteHost) PlanProjectClone(ctx context.Context, req hostsvc.ProjectClone) (*hostsvc.ProjectClone, error) {
	return ptr(hostCall[hostsvc.ProjectClone](ctx, h.conn, "PlanProjectClone", req))
}

func (h *remoteHost) CloneProject(ctx context.Context, req hostsvc.ProjectClone) error {
	return hostExec(ctx, h.conn, "CloneProject", req)
}

func (s *Server) PlanProjectClone(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var args hostsvc.ProjectClone
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	return jsonResp(s.host.PlanProjectClone(ctx, args))
}

func (s *Server) CloneProject(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	var args hostsvc.ProjectClone
	if err := unmarshalJSON(req.Payload, &args); err != nil {
		return nil, err
	}
	if err := requireAbsoluteHostPath(args.Directory, "clone destination"); err != nil {
		return nil, err
	}
	return &pb.JsonResp{}, s.host.CloneProject(ctx, args)
}
