package local

import (
	"context"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

// RunOpenCodeHealthChecks owns startup recovery and periodic liveness scans.
// It never launches or stops a runtime and joins its probes on cancellation.
func (h *Host) RunOpenCodeHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	h.runOpenCodeHealthChecks(ctx, ticker.C)
}

func (h *Host) runOpenCodeHealthChecks(ctx context.Context, ticks <-chan time.Time) {
	defer module.StopSchedule(ctx, "managed-runtime-health")
	for ctx.Err() == nil {
		module.Scheduled(ctx, "managed-runtime-health", time.Minute, func(ctx context.Context) {
			if ports := h.RefreshManagedOpenCodePorts(ctx); ports == nil && ctx.Err() == nil {
				module.ReportError(ctx, errors.New("managed runtime health scan incomplete"))
				log.Warn("host: managed OpenCode health scan incomplete")
			}
		})
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
