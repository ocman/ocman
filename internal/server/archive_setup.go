package server

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

func (s *Server) shouldResurfaceSession(ctx context.Context, session db.Session, archivedAt int64, mode string) bool {
	if !db.ShouldResurfaceSession(session, archivedAt, mode) {
		return false
	}
	if session.PendingPermission || session.PendingQuestion {
		return true
	}
	return !s.sessionLifecycleModule().SetupPending(ctx, session.ID)
}
