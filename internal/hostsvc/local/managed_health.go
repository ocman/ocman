package local

import (
	"context"
	"time"

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
	for ctx.Err() == nil {
		if ports := h.RefreshManagedOpenCodePorts(ctx); ports == nil && ctx.Err() == nil {
			log.Warn("host: managed OpenCode health scan incomplete")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticks:
		}
	}
}
