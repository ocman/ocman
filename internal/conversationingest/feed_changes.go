package conversationingest

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

// Successful events leave the detached batch immediately. Only failed events
// are requeued, with their deletion flags, sequence and message IDs intact.
func (w *sourceWorker) applyChanges(ctx context.Context, pending map[string]*platforms.HistoryChange, dirty map[string]bool) error {
	ids := make([]string, 0, len(pending))
	for id := range pending {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.applyPendingChange(ctx, *pending[id], dirty[id]); err != nil {
			failures = append(failures, err)
			continue
		}
		delete(pending, id)
		delete(dirty, id)
	}
	for id := range dirty {
		if id == "" || pending[id] != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.refreshFeedSession(ctx, id, dirty[id]); err != nil {
			failures = append(failures, err)
			continue
		}
		delete(dirty, id)
	}
	return errors.Join(failures...)
}

func (w *sourceWorker) applyChange(ctx context.Context, c platforms.HistoryChange) error {
	store := w.module.deps.Store
	key := statev2.Key{InstanceID: w.instance.ID, SessionID: c.SessionID}
	version := db.SequenceVersion(c.Seq, true)
	ticket := w.module.readTicket(w.source.AdapterID, c.SessionID)
	stored, err := store.SessionState(ctx, key)
	if err != nil {
		return err
	}
	exists := stored.Revision > 0
	if !c.Deleted && exists && !stored.SourceDeleted && stored.ReadFormat == statev2.ConversationReadFormat {
		applied, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+c.SessionID)
		if err != nil {
			return err
		}
		if applied == version && !w.explicitReadPending(c.SessionID) {
			return nil
		}
	}
	switch {
	case c.Deleted:
		if err := w.resolveMissing(ctx, c.SessionID, ticket); err != nil {
			return err
		}
		return w.advanceSequence(ctx, c.SessionID, version)
	case c.Resync || !exists || stored.SourceDeleted || stored.ReadFormat != statev2.ConversationReadFormat:
		return w.requireComplete(w.execute(ctx, pass{reads: []db.HistoryEntry{{ID: c.SessionID, Version: version}}}, nil))
	}
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	snapshot, err := w.feed.HistoryMessages(readCtx, c.SessionID, uniqueSorted(c.MessageIDs))
	cancel()
	if err != nil {
		if missingSession(err) {
			gone, checkErr := w.reconcileMissing(ctx, c.SessionID, ticket)
			if checkErr != nil || gone {
				return checkErr
			}
		}
		return err
	}
	batch, _, err := Normalize(key, snapshot)
	if err != nil {
		return err
	}
	current, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+c.SessionID)
	if err != nil {
		return err
	}
	if current == version {
		return w.approveRead(ctx, c.SessionID, version, ticket)
	}
	if _, err := store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: w.instance.ID,
		Checkpoint: statev2.CheckpointUpdate{Stream: sequencePrefix + c.SessionID, ExpectedCursor: current, Cursor: version},
		Batches:     []statev2.StreamBatch{{Session: &batch.Session, Messages: batch.Messages, Parts: batch.Parts}},
		SourceState: map[string]string{readReadyPrefix + c.SessionID: version}}); err != nil {
		return err
	}
	w.readReady(c.SessionID, ticket)
	return nil
}

func (w *sourceWorker) advanceSequence(ctx context.Context, sessionID, version string) error {
	store := w.module.deps.Store
	current, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+sessionID)
	if err != nil || current == version {
		return err
	}
	_, err = store.ApplyStream(ctx, statev2.StreamUpdate{InstanceID: w.instance.ID,
		Checkpoint: statev2.CheckpointUpdate{Stream: sequencePrefix + sessionID, ExpectedCursor: current, Cursor: version}})
	return err
}

func (w *sourceWorker) sessionVersion(ctx context.Context, id string) (string, error) {
	if reader, ok := w.feed.(platforms.HistorySequenceReader); ok {
		seq, ok, err := reader.HistorySequence(ctx, id)
		if err != nil {
			return "", err
		}
		return db.SequenceVersion(seq, ok), nil
	}
	sequences, err := w.feed.HistorySequences(ctx)
	if err != nil {
		return "", err
	}
	seq, ok := sequences[id]
	return db.SequenceVersion(seq, ok), nil
}

func uniqueSorted(ids []string) []string {
	out := append([]string{}, ids...)
	slices.Sort(out)
	return slices.Compact(out)
}
