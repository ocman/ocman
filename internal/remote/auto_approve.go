package remote

import (
	"context"
	"encoding/json"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/platforms"
	pb "github.com/ocman/ocman/internal/remote/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AutoApproveHandler reads, and with a non-nil enabled sets, an owner-local
// session's auto-approve toggle.
type AutoApproveHandler func(context.Context, platforms.Platform, string, *bool) (autoapprove.AutoApproveState, error)

func (s *Server) UseAutoApprove(handler AutoApproveHandler) *Server {
	s.autoApprove = handler
	return s
}

type autoApproveRequest struct {
	SessionID string `json:"sessionId"`
	Enabled   *bool  `json:"enabled,omitempty"`
}

// SessionAutoApprove serves the hub's per-session toggle. The owner stores it
// because the owner's judge is the one that reads it.
func (s *Server) SessionAutoApprove(ctx context.Context, req *pb.CallReq) (*pb.JsonResp, error) {
	if s.autoApprove == nil {
		return nil, status.Error(codes.Unimplemented, "session auto-approve unavailable")
	}
	adapter, err := s.platformFor(req.Platform)
	if err != nil {
		return nil, err
	}
	if remoteID, _ := platforms.SplitPlatformID(string(adapter.ID())); remoteID != "" {
		return nil, status.Error(codes.InvalidArgument, "auto-approve requires an owner-local platform")
	}
	var input autoApproveRequest
	if len(req.Payload) > 1<<10 || json.Unmarshal(req.Payload, &input) != nil || input.SessionID == "" {
		return nil, status.Error(codes.InvalidArgument, "invalid auto-approve request")
	}
	if s.registry.CanonicalSessionIDs() {
		if err := validateCanonicalReferences(input.SessionID, "", true); err != nil {
			return nil, err
		}
	}
	if _, err := platforms.ReadSessionSummary(ctx, adapter, input.SessionID); err != nil {
		return nil, svcErr(err)
	}
	result, err := s.autoApprove(ctx, adapter, input.SessionID, input.Enabled)
	return jsonResp(result, svcErr(err))
}

func (p *remotePlatform) SessionAutoApprove(ctx context.Context, id string, enabled *bool) (autoapprove.AutoApproveState, error) {
	payload, err := p.call(ctx, "SessionAutoApprove", "", autoApproveRequest{SessionID: id, Enabled: enabled})
	if err != nil {
		return autoapprove.AutoApproveState{}, remotePlatformError(err)
	}
	var result autoapprove.AutoApproveState
	err = json.Unmarshal(payload, &result)
	return result, err
}
