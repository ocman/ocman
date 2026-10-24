package conversationingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

type importResult struct {
	outcome string // imported, unchanged or deferred
	version string
	usage   statev2.Usage
}

// Both cursors commit in the content transaction. A crash cannot advance the
// transcript byte offset past uncommitted messages, even before the journal ack.
type snapshotCursor struct {
	Digest string
	Source string
}

func (w *sourceWorker) importSession(ctx context.Context, entry db.HistoryEntry, full ...bool) (importResult, error) {
	store := w.module.deps.Store
	if err := w.clearReadReceipt(ctx, entry.ID); err != nil {
		return importResult{}, err
	}
	key := statev2.Key{InstanceID: w.instance.ID, SessionID: entry.ID}
	stored, err := store.SessionState(ctx, key)
	if err != nil {
		return importResult{}, err
	}
	cursor, err := store.Checkpoint(ctx, key.InstanceID, snapshotStream(entry.ID))
	if err != nil {
		return importResult{}, err
	}
	var saved snapshotCursor
	_ = json.Unmarshal([]byte(cursor), &saved) // legacy fingerprints have no source cursor
	if stored.ReadFormat != statev2.ConversationReadFormat || len(full) > 0 && full[0] {
		saved.Source = ""
	}
	snapshot, err := w.sourceSnapshot(ctx, entry.ID, saved.Source)
	if errors.Is(err, db.ErrHistoryChanged) {
		w.forget(ctx, entry.ID)
		return importResult{outcome: "deferred"}, nil
	}
	if err != nil {
		return importResult{}, fmt.Errorf("reading source history: %w", err)
	}
	if snapshot != nil && snapshot.SourceCursor != "" && snapshot.SourceCursor == saved.Source {
		if err := w.confirmSnapshot(ctx, entry.ID, snapshot); err != nil {
			return importResult{}, err
		}
		return w.rememberSnapshot(ctx, key, importResult{"unchanged", snapshot.Version, statev2.Usage{}}, true)
	}
	batch, digest, err := Normalize(key, snapshot)
	if err != nil {
		return importResult{}, err
	}
	next := digest
	if snapshot.SourceCursor != "" {
		raw, err := json.Marshal(snapshotCursor{Digest: digest, Source: snapshot.SourceCursor})
		if err != nil {
			return importResult{}, err
		}
		next = string(raw)
	}
	version := snapshot.Version
	if w.feed != nil {
		version = entry.Version
	}
	result := importResult{"unchanged", version, expectedUsage(batch)}
	if next != cursor || stored.ReadFormat != statev2.ConversationReadFormat {
		batch.ExpectedRevision = stored.Revision
		if next != cursor {
			batch.Checkpoint = &statev2.CheckpointUpdate{Stream: snapshotStream(entry.ID), ExpectedCursor: cursor, Cursor: next}
		}
		committed, err := store.Commit(ctx, batch)
		if err != nil {
			return importResult{}, fmt.Errorf("committing conversation history: %w", err)
		}
		if !committed.Duplicate {
			result.outcome = "imported"
		}
	}
	if err := w.confirmSnapshot(ctx, entry.ID, snapshot); err != nil {
		return importResult{}, err
	}
	return w.rememberSnapshot(ctx, key, result, snapshot.Partial)
}

func (w *sourceWorker) confirmSnapshot(ctx context.Context, id string, snapshot *db.HistorySnapshot) error {
	observer, ok := w.source.Reader.(platforms.HistoryCommitObserver)
	if !ok {
		return nil
	}
	confirm, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	defer cancel()
	return observer.HistoryCommitted(confirm, id, snapshot)
}

func (w *sourceWorker) sourceSnapshot(ctx context.Context, id, cursor string) (*db.HistorySnapshot, error) {
	if w.journaled {
		if reader, ok := w.source.Reader.(platforms.HistoryDeltaReader); ok {
			readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
			defer cancel()
			snapshot, err := reader.HistoryDelta(readCtx, id, cursor)
			if !errors.Is(err, platforms.ErrUnsupported) {
				return snapshot, err
			}
		}
	}
	return w.snapshot(ctx, id)
}
