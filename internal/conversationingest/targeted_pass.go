package conversationingest

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// Known-session events are targeted updates, not requests to inventory the source.
// Unknown identities and gaps still require a complete discovery pass.
func (w *sourceWorker) runTargetedPass(ctx context.Context, dirty map[string]bool) error {
	if dirty[""] || !w.loaded {
		return w.runPass(ctx, dirty)
	}
	if err := w.ensureInstance(ctx); err != nil {
		return err
	}
	p := pass{routine: true}
	for id, force := range dirty {
		p.full = p.full || force
		if _, known := w.versions[id]; !known {
			return w.runPass(ctx, dirty)
		}
	}
	for id := range dirty {
		p.reads = append(p.reads, db.HistoryEntry{ID: id, Version: w.versions[id]})
	}
	return w.runPrepared(ctx, p)
}
