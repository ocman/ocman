package conversationingest

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// Known-session hooks are targeted updates, not requests to inventory the source.
// Unknown identities and gaps still require a complete discovery pass.
func (w *sourceWorker) runJournalPass(ctx context.Context, dirty map[string]bool) error {
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
	seen := map[string]bool{}
	for id := range dirty {
		family, err := w.module.deps.Store.Descendants(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: id})
		if err != nil {
			return err
		}
		for _, member := range family {
			if !seen[member] {
				seen[member] = true
				p.reads = append(p.reads, db.HistoryEntry{ID: member, Version: w.versions[member]})
			}
		}
	}
	return w.runPrepared(ctx, p)
}
