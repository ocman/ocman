package conversationingest

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/statev2"
)

func (w *sourceWorker) applyPendingChange(ctx context.Context, change platforms.HistoryChange, force bool) error {
	// Named-message updates cannot validate a complete repair: the reply that
	// caused the invalidation may not be named by the coalesced feed events.
	if !change.Deleted && (force || w.explicitReadPending(change.SessionID)) {
		return w.refreshFeedSession(ctx, change.SessionID, true)
	}
	return w.applyChange(ctx, change)
}

func (w *sourceWorker) refreshFeedSession(ctx context.Context, id string, force bool) error {
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	defer cancel()
	version, err := w.sessionVersion(readCtx, id)
	if err != nil {
		return err
	}
	store := w.module.deps.Store
	applied, err := store.Checkpoint(ctx, w.instance.ID, sequencePrefix+id)
	if err != nil {
		return err
	}
	receipt, err := store.SourceValue(ctx, w.instance.ID, "read-ready/"+id)
	if err != nil {
		return err
	}
	if !force && !w.explicitReadPending(id) && applied == version && receipt == version {
		s, err := store.SessionState(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: id})
		if err != nil {
			return err
		}
		if s.ReadFormat == statev2.ConversationReadFormat && !s.SourceDeleted {
			return nil
		}
	}
	return w.requireComplete(w.execute(ctx, pass{reads: []db.HistoryEntry{{ID: id, Version: version}}}, nil))
}

func (w *sourceWorker) explicitReadPending(id string) bool {
	m := w.module
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	key := [2]string{w.source.AdapterID, id}
	return m.readRequests[key] > m.readValidated[key]
}
