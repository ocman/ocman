package owned

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// SessionObservations uses the same native cache and background overlay as
// conversation reads, translating the complete snapshot to canonical IDs.
func (a *Adapter) SessionObservations(ctx context.Context, ids []string) (map[string]platforms.LiveState, error) {
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	if source, ok := a.native.(platforms.SessionObservationReadiness); ok {
		connected, err := source.SessionObservationsReady(ctx)
		if err != nil {
			return nil, err
		}
		a.background.mu.RLock()
		ready := a.background.ready
		a.background.mu.RUnlock()
		if connected && !ready {
			return nil, platforms.ErrHistoryUnavailable
		}
	}
	rows, err := a.storedIdentities(ctx, ids)
	if err != nil {
		return nil, err
	}
	observe, err := a.conversationObservations(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]platforms.LiveState, len(rows))
	for id, row := range rows {
		if live := observe(row.NativeID); live != nil {
			out[id] = *live
		}
	}
	return out, nil
}
