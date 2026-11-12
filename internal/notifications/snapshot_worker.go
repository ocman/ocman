package notifications

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
	log "github.com/sirupsen/logrus"
)

// Return snapshot errors through the observer rather than both reporting and
// logging them inside module.Every. That produced a second scheduler warning
// and, without an observer, could not propagate the failed iteration at all.
func (m *Module) runSnapshots(ctx context.Context, interval time.Duration, wake chan<- struct{}) {
	defer module.StopSchedule(ctx, "web-push")
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		module.RunWithRecover("web-push", func() {
			started := time.Now()
			err := module.RunScheduled(ctx, module.Schedule{Name: "web-push", Title: module.ScheduleTitle("web-push"), Interval: interval}, m.tick)
			if err != nil && ctx.Err() == nil {
				log.WithError(err).WithFields(log.Fields{"job": "web-push", "elapsed_ms": time.Since(started).Milliseconds()}).Warn("checking Web Push notifications")
			}
			select {
			case wake <- struct{}{}:
			default:
			}
		})
		ticker.Reset(interval)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
