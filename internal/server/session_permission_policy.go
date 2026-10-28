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
