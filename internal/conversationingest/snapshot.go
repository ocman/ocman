package conversationingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

type importResult struct {
	outcome string // imported, unchanged, deferred or source-deleted
	version string
	// mismatch reports retained accounting that differs from the snapshot;
	// the read receipt and version hint were withheld in the same commit.
	mismatch bool
}

// Both cursors commit in the content transaction. A crash cannot advance the
// transcript byte offset past uncommitted messages, even before the journal ack.
type snapshotCursor struct {
	Digest string
	Source string
}

func (w *sourceWorker) importSession(ctx context.Context, entry db.HistoryEntry, full ...bool) (importResult, error) {
	ticket := w.module.readTicket(w.source.AdapterID, entry.ID)
	store := w.module.deps.Store
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
	fullRead := stored.ReadFormat != statev2.ConversationReadFormat || len(full) > 0 && full[0]
	if fullRead {
		saved.Source = ""
	}
	snapshot, err := w.sourceSnapshot(ctx, entry.ID, saved.Source, fullRead)
	if errors.Is(err, db.ErrHistoryChanged) {
		w.withdraw(ctx, entry.ID)
		return importResult{outcome: "deferred"}, nil
	}
	if err != nil {
		if missingSession(err) {
			gone, checkErr := w.reconcileMissing(ctx, entry.ID, ticket)
			if checkErr != nil {
				return importResult{}, checkErr
			}
			if gone {
				return importResult{outcome: "source-deleted"}, nil
			}
		}
		return importResult{}, fmt.Errorf("reading source history: %w", err)
	}
	if snapshot != nil && snapshot.SourceCursor != "" && snapshot.SourceCursor == saved.Source {
		if err := w.confirmSnapshot(ctx, entry.ID, snapshot); err != nil {
			return importResult{}, err
		}
		// Runtime bootstrap cannot validate absent content. Keep the committed
		// accounting and every previously retained message, unverified.
		return w.recordSource(ctx, key, importResult{outcome: "unchanged", version: snapshot.Version}, nil)
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
	result := importResult{outcome: "unchanged", version: version}
	var verify *statev2.Usage
	if !snapshot.Partial {
		usage := expectedUsage(batch)
		verify = &usage
	}
	if next != cursor || stored.ReadFormat != statev2.ConversationReadFormat {
		batch.ExpectedRevision = stored.Revision
		if next != cursor {
			batch.Checkpoint = &statev2.CheckpointUpdate{Stream: snapshotStream(entry.ID), ExpectedCursor: cursor, Cursor: next}
		}
		batch.Source = w.sourceUpdate(entry.ID, version, verify)
		committed, err := store.Commit(ctx, batch)
		if err != nil {
			return importResult{}, fmt.Errorf("committing conversation history: %w", err)
		}
		if !committed.Duplicate {
			result.outcome = "imported"
		}
		result.mismatch = committed.UsageMismatch
		w.noteVersion(entry.ID, result)
		if err := w.confirmSnapshot(ctx, entry.ID, snapshot); err != nil {
			return importResult{}, err
		}
		return result, nil
	}
	if err := w.confirmSnapshot(ctx, entry.ID, snapshot); err != nil {
		return importResult{}, err
	}
	return w.recordSource(ctx, key, result, verify)
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

func (w *sourceWorker) sourceSnapshot(ctx context.Context, id, cursor string, full bool) (*db.HistorySnapshot, error) {
	if w.journaled || !full {
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
