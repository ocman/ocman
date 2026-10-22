package conversationingest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const (
	// journalCheck is how often the journal directory is checked for new
	// records: one directory read and a stat per segment, no source reads.
	journalCheck = 500 * time.Millisecond
	journalPrune = time.Hour
	// journalIdle keeps fully consumed segments this long, so another ocman
	// store on the machine that reads the same journal can consume them too.
	journalIdle = 7 * 24 * time.Hour
)

func journalDir(reader platforms.HistoryReader) string {
	if j, ok := reader.(platforms.HistoryJournal); ok {
		return j.HistoryJournalDir()
	}
	return ""
}

// runJournal keeps a source current from its hook journal. Startup compares
// the source's cheap version hints once, which also covers anything changed
// while ocman (or a hook) was not running. Afterwards only journal records
// trigger reads, each refreshing its session; there is no periodic polling.
func (w *sourceWorker) runJournal(ctx context.Context, dir string) {
	w.journaled = true
	consumer := &journalConsumer{store: w.module.deps.Store, dir: dir, refresh: w.refreshJournal, pruneIdle: journalIdle,
		onGap: func(string) { w.module.Notify(w.source.AdapterID, "") }}
	if reader, ok := w.source.Reader.(platforms.HistoryJournalReader); ok {
		consumer.read = reader.HistoryJournalEntries
	}
	sizes := map[string]int64{}
	// Readiness expires after two poll intervals, so check at least that often.
	check := time.NewTicker(min(journalCheck, w.module.deps.PollInterval))
	defer check.Stop()
	prune := time.NewTicker(journalPrune)
	defer prune.Stop()
	sweep, drain := true, true
	var retry retryBackoff
	for {
		if (sweep || drain) && retry.due() {
			var err error
			if sweep {
				err = w.sweepChecked(ctx)
				if w.last.Counters["deferred"] > 0 {
					err = errDeferred
				}
				if err == nil {
					sweep = false
				}
			}
			if err == nil && drain && w.instance.ID != "" {
				consumer.instanceID = w.instance.ID
				_, err = consumer.drain(ctx)
			}
			if err != nil {
				w.readiness(false)
				var translation *journalTranslationError
				if !sweep && !errors.As(err, &translation) {
					_ = w.recordFailure(ctx, err, w.last)
				}
				retry.fail()
			} else {
				drain = false
				w.lastFailure = ""
				retry = retryBackoff{}
				w.readiness(true)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-check.C:
			grew, err := journalGrew(dir, sizes)
			if err != nil {
				w.readiness(false)
				_ = w.recordFailure(ctx, err, w.last)
				drain = true
				if retry.due() {
					retry.fail()
				}
				continue
			}
			drain = drain || grew
			if !drain && !sweep && w.lastFailure == "" {
				w.readiness(true) // current: nothing new was recorded
			}
		case <-prune.C:
			if w.instance.ID != "" {
				consumer.instanceID = w.instance.ID
				_ = consumer.prune(ctx)
			}
		case <-w.wake:
			// Explicit repairs and waiting reads.
			sweep, drain = true, true
			retry = retryBackoff{}
		}
	}
}

// journalGrew reports whether any segment changed size since the last check.
func journalGrew(dir string, sizes map[string]int64) (bool, error) {
	segments, err := historyjournal.Segments(dir)
	if err != nil {
		return false, fmt.Errorf("listing history journal: %w", err)
	}
	grew := false
	seen := map[string]bool{}
	for _, segment := range segments {
		seen[segment] = true
		info, err := os.Stat(filepath.Join(dir, segment))
		if err != nil {
			return false, fmt.Errorf("checking history journal segment %s: %w", segment, err)
		}
		if sizes[segment] != info.Size() {
			sizes[segment] = info.Size()
			grew = true
		}
	}
	for segment := range sizes {
		if !seen[segment] {
			delete(sizes, segment)
		}
	}
	return grew, nil
}

// runPass owns translation receipts. Wrapping for segment context must not
// create a second signature that alternates with it on every retry.
type journalTranslationError struct{ error }

func (e *journalTranslationError) Unwrap() error { return e.error }

// refreshJournal rereads the sessions named by hooks, including changed
// subagent transcripts. The source transcript remains the durable content.
func (w *sourceWorker) refreshJournal(ctx context.Context, records []historyjournal.Record) error {
	dirty := map[string]bool{}
	for _, rec := range records {
		dirty[rec.Session] = rec.Session == ""
		if rec.Type == "SubagentStop" {
			dirty[""] = true
		}
	}
	err := w.runJournalPass(ctx, dirty)
	if err == nil && w.last.Counters["deferred"] > 0 {
		err = errDeferred
	}
	if err != nil {
		return &journalTranslationError{err}
	}
	return nil
}
