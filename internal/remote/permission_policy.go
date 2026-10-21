package remote

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms"
	pb "github.com/NoUseFreak/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type PermissionPolicyHandler func(context.Context, platforms.Platform, string, *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error)

func (s *Server) UsePermissionPolicy(handler PermissionPolicyHandler) *Server {
	s.permissionPolicy = handler
	return s
}

type permissionPolicyRequest struct {
	SessionID string                      `json:"sessionId"`
	Rules     *[]platforms.PermissionRule `json:"rules,omitempty"`
}

func (s *Server) SessionPermissionPolicy(ctx context.Context, req *pb.PlatformJsonReq) (*pb.JsonResp, error) {
	if s.permissionPolicy == nil {
		return nil, status.Error(codes.Unimplemented, "session permission policy unavailable")
	}
	adapter, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	if remoteID, _ := platforms.SplitPlatformID(string(adapter.ID())); remoteID != "" {
		return nil, status.Error(codes.InvalidArgument, "policy requires an owner-local platform")
	}
	var input permissionPolicyRequest
	if len(req.Payload) > 1<<20 || json.Unmarshal(req.Payload, &input) != nil || input.SessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid permission policy request")
	}
	if s.registry.CanonicalSessionIDs() {
		if err := validateCanonicalReferences(input.SessionID, "", true); err != nil {
			return nil, err
		}
	}
	if _, err := platforms.ReadSessionSummary(ctx, adapter, input.SessionID); err != nil {
		return nil, svcErr(err)
	}
	result, err := s.permissionPolicy(ctx, adapter, input.SessionID, input.Rules)
	return jsonResp(result, svcErr(err))
}

func (p *remotePlatform) SessionPermissionPolicy(ctx context.Context, id string, rules *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error) {
	client := p.conn.Client()
	if client == nil {
		return nil, platforms.ErrRemoteOffline
	}
	if rules != nil && *rules == nil {
		empty := []platforms.PermissionRule{}
		rules = &empty
	}
	payload, err := json.Marshal(permissionPolicyRequest{SessionID: id, Rules: rules})
	if err != nil {
		return nil, err
	}
	resp, err := client.SessionPermissionPolicy(ctx, &pb.PlatformJsonReq{Platform: p.base, Payload: payload})
	if err != nil {
		return nil, remotePlatformError(err)
	}
	var result autoapprove.PermissionPolicy
	if err := json.Unmarshal(resp.Payload, &result); err != nil {
		return nil, err
	}
	for i := range result.Requests {
		result.Requests[i].Platform = string(p.ID())
	}
	return &result, nil
}
