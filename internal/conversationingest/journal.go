package conversationingest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/historyjournal"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

// JournalTranslator converts one segment's ordered records into incremental
// session updates. It applies payloads directly and may read only what a
// record lacks (for example missing session metadata) from its own source.
// An error is transient: the records are retried, never skipped.
type JournalTranslator interface {
	Translate(ctx context.Context, instanceID string, records []historyjournal.Record) ([]statev2.StreamBatch, error)
}

// journalConsumer applies hook-written journal segments. Each segment's offset
// commits in the same transaction as the history it produced, so a restart
// resumes exactly where the last commit stopped.
type journalConsumer struct {
	store      *statev2.DB
	instanceID string
	dir        string
	translator JournalTranslator
	batchSize  int
	pruneIdle  time.Duration
	// onGap requests a source repair when a segment lost unread events.
	onGap func(segment string)
}

func journalStream(segment string) string { return "journal/" + segment }

// drain applies every complete record currently in the journal.
func (c *journalConsumer) drain(ctx context.Context) (int, error) {
	segments, err := historyjournal.Segments(c.dir)
	if err != nil {
		return 0, err
	}
	applied := 0
	for _, segment := range segments {
		n, err := c.drainSegment(ctx, segment)
		applied += n
		if err != nil {
			return applied, err
		}
	}
	return applied, nil
}

func (c *journalConsumer) drainSegment(ctx context.Context, segment string) (int, error) {
	applied := 0
	for ctx.Err() == nil {
		stored, err := c.store.Checkpoint(ctx, c.instanceID, journalStream(segment))
		if err != nil {
			return applied, err
		}
		offset, _ := strconv.ParseInt(stored, 10, 64)
		entries, err := historyjournal.ReadSegment(c.dir, segment, offset, c.size())
		if errors.Is(err, historyjournal.ErrGap) {
			if err := c.restart(ctx, segment, stored); err != nil {
				return applied, err
			}
			continue
		}
		if err != nil {
			return applied, err
		}
		var records []historyjournal.Record
		end := offset
		for _, e := range entries {
			end = e.End
			if e.Err != nil {
				log.WithError(e.Err).WithFields(log.Fields{"segment": segment, "offset": e.Offset}).Warn("conversation journal: skipping unusable record")
				continue
			}
			records = append(records, e.Record)
		}
		if end == offset {
			return applied, nil
		}
		var batches []statev2.StreamBatch
		if len(records) > 0 {
			if batches, err = c.translator.Translate(ctx, c.instanceID, records); err != nil {
				return applied, fmt.Errorf("translating %s: %w", segment, err)
			}
		}
		if _, err := c.store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: c.instanceID, Batches: batches,
			Checkpoint: statev2.CheckpointUpdate{Stream: journalStream(segment), ExpectedCursor: stored, Cursor: strconv.FormatInt(end, 10)}}); err != nil {
			return applied, err
		}
		applied += len(records)
	}
	return applied, ctx.Err()
}

// restart consumes a truncated or replaced segment from its beginning after
// requesting a repair for whatever it lost. The reset commits like any cursor.
func (c *journalConsumer) restart(ctx context.Context, segment, stored string) error {
	log.WithField("segment", segment).Warn("conversation journal: segment lost unread events; repairing from source")
	if c.onGap != nil {
		c.onGap(segment)
	}
	_, err := c.store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: c.instanceID,
		Checkpoint: statev2.CheckpointUpdate{Stream: journalStream(segment), ExpectedCursor: stored, Cursor: "0"}})
	return err
}

// prune removes segments that are fully applied and no longer written.
func (c *journalConsumer) prune(ctx context.Context) error {
	segments, err := historyjournal.Segments(c.dir)
	if err != nil {
		return err
	}
	cursors := map[string]int64{}
	for _, segment := range segments {
		stored, err := c.store.Checkpoint(ctx, c.instanceID, journalStream(segment))
		if err != nil {
			return err
		}
		cursors[segment], _ = strconv.ParseInt(stored, 10, 64)
	}
	idle := c.pruneIdle
	if idle <= 0 {
		idle = time.Hour
	}
	_, err = historyjournal.Prune(c.dir, cursors, idle)
	return err
}

func (c *journalConsumer) size() int {
	if c.batchSize > 0 {
		return c.batchSize
	}
	return 500
}
