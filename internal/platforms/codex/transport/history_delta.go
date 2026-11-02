package codextransport

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// Native turns/items remain complete snapshots. Ordinary updates may reuse or
// extend the rollout-accounting cache; HistorySnapshot forces verification.
func (p *Platform) HistoryDelta(ctx context.Context, id, _ string) (*db.HistorySnapshot, error) {
	r, err := p.history.reader(ctx)
	if err != nil {
		return nil, err
	}
	return r.CachedSnapshot(ctx, id)
}
