package remote

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/module"
)

func (m *Manager) stopInventoryObservation(ctx context.Context) {
	module.StopSchedule(ctx, "remote-project-inventory")
}

func (m *Manager) observeInventories(ctx context.Context, interval time.Duration) {
	module.Scheduled(ctx, "remote-project-inventory", interval, func(ctx context.Context) {
		if m.refreshInventories != nil {
			m.refreshInventories(ctx)
		} else {
			m.RefreshInventories(ctx)
		}
	})
}
