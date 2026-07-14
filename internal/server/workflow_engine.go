package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	workflowTriggerTickInterval = 5 * time.Second
	// workflowArtifactCleanupInterval bounds how often expired artifact
	// payloads are swept. Retention is a coarse (day-scale) policy, so an
	// hourly sweep is ample and keeps the tick loop cheap.
	workflowArtifactCleanupInterval = time.Hour
)

func (s *Server) runWorkflowTriggerEngine(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	ticker := time.NewTicker(workflowTriggerTickInterval)
	defer ticker.Stop()
	evaluate := func() {
		if err := s.workflowSvc().EvaluateTriggers(ctx); err != nil {
			log.WithError(err).Warn("workflow-trigger-engine: evaluate")
		}
	}
	var lastCleanup time.Time
	cleanup := func() {
		if time.Since(lastCleanup) < workflowArtifactCleanupInterval {
			return
		}
		lastCleanup = time.Now()
		removed, err := s.workflowSvc().CleanupExpiredPayloads(ctx)
		if err != nil {
			log.WithError(err).Warn("workflow-trigger-engine: artifact cleanup")
			return
		}
		if removed > 0 {
			log.WithField("removed", removed).Info("workflow-trigger-engine: cleaned expired artifact payloads")
		}
	}
	runWithRecover("workflow-trigger-engine", evaluate)
	runWithRecover("workflow-artifact-cleanup", cleanup)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("workflow-trigger-engine", evaluate)
			runWithRecover("workflow-artifact-cleanup", cleanup)
		}
	}
}
