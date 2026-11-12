package server

import (
	"context"
	"fmt"
	"time"

	"github.com/ocman/ocman/internal/db"
)

func (s *Server) setupArchiveWatermark(ctx context.Context, platform, id string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	p, ok := s.adapterForSession(ctx, platform, id)
	if !ok {
		return 0, fmt.Errorf("session owner unavailable")
	}
	detail, err := p.Session(ctx, id, 0, 0)
	if err != nil {
		return 0, err
	}
	if detail == nil || detail.Session == nil || detail.Session.TimeUpdated <= 0 {
		return 0, fmt.Errorf("session update watermark unavailable")
	}
	return detail.Session.TimeUpdated, nil
}

func (s *Server) holdArchivedSetup(ctx context.Context, sessions []db.Session) error {
	if s.stateDB == nil || len(sessions) == 0 {
		return nil
	}
	pending, err := s.sessionLifecycleModule().ArchivedSetupIDs(ctx)
	if err != nil {
		return err
	}
	for i := range sessions {
		row := &sessions[i]
		if pending[row.ID] && !row.PendingPermission && !row.PendingQuestion {
			row.Archived = true
		}
	}
	return nil
}

func (s *Server) shouldResurfaceSession(ctx context.Context, session db.Session, archivedAt int64, mode string) bool {
	if !db.ShouldResurfaceSession(session, archivedAt, mode) {
		return false
	}
	if session.PendingPermission || session.PendingQuestion {
		return true
	}
	return !s.sessionLifecycleModule().SetupPending(ctx, session.ID)
}
