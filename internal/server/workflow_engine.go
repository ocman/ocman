package server

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

const workflowTriggerTickInterval = 5 * time.Second

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
	runWithRecover("workflow-trigger-engine", evaluate)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runWithRecover("workflow-trigger-engine", evaluate)
		}
	}
}
