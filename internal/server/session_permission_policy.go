package server

import (
	"context"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/platforms"
)

func permissionPolicyParent(ctx context.Context, adapter platforms.Platform, id string) (string, error) {
	session, err := platforms.ReadSessionSummary(ctx, adapter, id)
	if err != nil {
		return "", err
	}
	if session == nil {
		return "", platforms.ErrNotFound
	}
	return session.ParentID, nil
}

func (s *Server) SessionPermissionPolicy(ctx context.Context, adapter platforms.Platform, id string, rules *[]platforms.PermissionRule) (*autoapprove.PermissionPolicy, error) {
	return s.autoapproveModule().SessionPermissionPolicy(ctx, adapter, id, rules)
}

// SessionAutoApprove serves the owner side of a hub's per-session toggle.
func (s *Server) SessionAutoApprove(ctx context.Context, adapter platforms.Platform, id string, enabled *bool) (autoapprove.AutoApproveState, error) {
	return s.autoapproveModule().SessionAutoApprove(ctx, adapter, id, enabled)
}
