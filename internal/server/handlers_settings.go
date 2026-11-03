package server

import (
	"context"

	"github.com/ocman/ocman/internal/httpx"
)

type worktreeInheritBody struct {
	Enabled bool `json:"enabled"`
}

func (s *Server) hasStateDB() bool { return s.stateDB != nil }

// worktreeInheritSetting serves /api/settings/worktree-inherit-permissions
// (issue #101): {"enabled": bool}, enabled when unset.
func (s *Server) worktreeInheritSetting() httpx.SettingRoute[worktreeInheritBody] {
	return httpx.SettingRoute[worktreeInheritBody]{
		Name:      "worktree inherit-permissions setting",
		Available: s.hasStateDB,
		Get: func(ctx context.Context) (any, error) {
			on, err := s.stateDB.GetWorktreeInheritPermissions(ctx)
			return worktreeInheritBody{on}, err
		},
		Set: func(ctx context.Context, body worktreeInheritBody) error {
			return s.stateDB.SetWorktreeInheritPermissions(ctx, body.Enabled)
		},
	}
}
