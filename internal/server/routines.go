package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

const routineTickInterval = 5 * time.Second

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
