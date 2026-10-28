package server

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/git"
	log "github.com/sirupsen/logrus"
)

func (s *Server) persistSidebarBranches(ctx context.Context, owner string, infos map[string]git.Info, observedAt int64) {
	if owner != "local" || s.historyAnalytics == nil || s.historyAnalytics.Store == nil {
		return
	}
	// A cache write must not queue a Git-info response behind ingestion.
	ctx, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	for directory, info := range infos {
		if err := s.historyAnalytics.Store.SetSidebarBranch(ctx, s.historyOwner, directory, info.Branch, observedAt); err != nil {
			log.WithError(err).Debug("saving sidebar branch observation")
			return
		}
	}
}
