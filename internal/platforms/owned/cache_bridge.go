package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) InstancePorts() platforms.InstancePorts {
	if provider, ok := a.native.(platforms.InstancePortProvider); ok {
		return provider.InstancePorts()
	}
	return nil
}

// MarkSessionDirty asks ingestion to reread one session; there is no native
// browsing cache to invalidate.
func (a *Adapter) MarkSessionDirty(id string) {
	if a.historyNotify != nil {
		a.historyNotify(id)
	}
}

func (a *Adapter) RefreshSession(ctx context.Context, id string) error {
	return a.refreshStored(ctx, id)
}
