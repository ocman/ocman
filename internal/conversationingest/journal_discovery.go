package conversationingest

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/tasksvc"
)

func (w *sourceWorker) discoverJournal(ctx context.Context, dirty map[string]bool) error {
	reader, ok := w.source.Reader.(platforms.HistoryFamilyReader)
	_, sourceWide := dirty[""]
	if !ok || sourceWide || !w.loaded {
		return w.runPass(ctx, dirty)
	}
	p := pass{}
	seen := map[string]bool{}
	for id := range dirty {
		if seen[id] {
			continue
		}
		readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
		entries, err := reader.HistoryFamilyInventory(readCtx, id)
		cancel()
		deferred, err := inventoryDeferrals(err)
		p.deferred += deferred
		if errors.Is(err, platforms.ErrUnsupported) || missingSession(err) {
			return w.runPass(ctx, dirty)
		}
		if err != nil {
			return w.recordFailure(ctx, err, tasksvc.Progress{})
		}
		for _, entry := range entries {
			if entry.ID == "" {
				return w.recordFailure(ctx, errors.New("family inventory contains an empty identity"), tasksvc.Progress{})
			}
			if seen[entry.ID] {
				continue
			}
			seen[entry.ID] = true
			version, known := w.versions[entry.ID]
			_, touched := dirty[entry.ID]
			if !known || version != entry.Version || touched {
				p.reads = append(p.reads, entry)
			} else {
				p.skipped++
			}
		}
		if !seen[id] {
			seen[id] = true
			p.reads = append(p.reads, db.HistoryEntry{ID: id, Version: w.versions[id]})
		}
	}
	p.routine = len(p.reads) == 1 && w.versions[p.reads[0].ID] != ""
	return w.runPrepared(ctx, p)
}
