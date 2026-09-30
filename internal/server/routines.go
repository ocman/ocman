package server

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

const routineTickInterval = 5 * time.Second

// webhookHistoryCleanupInterval spaces webhook history sweeps; retention is
// 30 days, so hourly keeps up without scanning on every inbox poll.
const webhookHistoryCleanupInterval = time.Hour

func (s *Server) runRoutines(ctx context.Context) {
	if s.routineSvc == nil {
		return
	}
	ticker := time.NewTicker(routineTickInterval)
	defer ticker.Stop()
	for {
		runWithRecover("routines", func() {
			if err := s.routineSvc.Tick(ctx); err != nil {
				log.WithError(err).Warn("routines: tick")
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// runWebhookHistoryCleanup expires webhook delivery rows and their files for
// the life of the process, independent of whether any inbox still exists: a
// revoked last inbox must not leave its delivery files behind forever.
func (s *Server) runWebhookHistoryCleanup(ctx context.Context) {
	ticker := time.NewTicker(webhookHistoryCleanupInterval)
	defer ticker.Stop()
	for {
		runWithRecover("webhook history cleanup", func() { s.cleanWebhookHistory(ctx) })
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) cleanWebhookHistory(ctx context.Context) {
	if err := s.stateDB.CleanupWebhookHistory(ctx, time.Now().Add(-state.WebhookHistoryRetention).UnixMilli()); err != nil {
		log.WithError(err).Warn("webhook history cleanup")
	}
}
