package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

func (s *Server) runRoutines(ctx context.Context) {
	if s.routineSvc == nil {
		return
	}
	ticker := time.NewTicker(promptScheduleTickInterval)
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
