package owned

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) HistoryJournalDir() string {
	if journal, ok := a.native.(platforms.HistoryJournal); ok {
		return journal.HistoryJournalDir()
	}
	return ""
}

func (a *Adapter) HistoryJournalEntries(ctx context.Context, segment string, offset int64, limit int) ([]historyjournal.Entry, error) {
	if err := a.checkSource(ctx); err != nil {
		return nil, err
	}
	dir := a.HistoryJournalDir()
	if dir == "" {
		return nil, platforms.ErrUnsupported
	}
	entries, err := historyjournal.ReadSegment(dir, segment, offset, limit)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		if entries[i].Err != nil {
			// Malformed lines are still skippable at their original offsets,
			// but never expose a partially decoded native session reference.
			entries[i].Record.Session = ""
			continue
		}
		row, err := a.canonical(ctx, entries[i].Record.Session)
		if errors.Is(err, platforms.ErrNotFound) {
			// A machine-wide journal may contain a new helper or another
			// configuration's hook. Refresh this source's own inventory instead
			// of assigning an unverified native ID to its physical source.
			entries[i].Record.Session = ""
			continue
		}
		if err != nil {
			return nil, err
		}
		entries[i].Record.Session = row.ID
	}
	return entries, a.checkSource(ctx)
}
