package conversationingest

import (
	"context"

	"github.com/ocman/ocman/internal/statev2"
)

const readReadyPrefix = "read-ready/"

func conversationRefreshes(sessions []statev2.Session) map[string]bool {
	refresh := map[string]bool{}
	for _, s := range sessions {
		if s.ReadFormat != statev2.ConversationReadFormat {
			refresh[s.SessionID] = true
		}
	}
	return refresh
}

// Read receipts are separate from scan hints: content is reader-ready only
// once its accounting is checked. sourceUpdate commits the hint, the receipt
// and the feed sequence with the content; a failed check removes both the
// receipt and the hint in that same transaction.
func (w *sourceWorker) sourceUpdate(id, version string, verify *statev2.Usage) *statev2.SourceUpdate {
	u := &statev2.SourceUpdate{
		State:    map[string]string{versionKey + id: "", readReadyPrefix + id: ""},
		Verify:   verify,
		Verified: map[string]string{versionKey + id: version, readReadyPrefix + id: version},
	}
	if w.feed != nil {
		u.Streams = map[string]string{sequencePrefix + id: version}
	}
	return u
}

// recordSource commits the bookkeeping for content that did not change.
func (w *sourceWorker) recordSource(ctx context.Context, key statev2.Key, result importResult, verify *statev2.Usage) (importResult, error) {
	mismatch, err := w.module.deps.Store.ApplySource(ctx, key, *w.sourceUpdate(key.SessionID, result.version, verify))
	if err != nil {
		return importResult{}, err
	}
	result.mismatch = mismatch
	w.noteVersion(key.SessionID, result)
	return result, nil
}

// noteVersion mirrors the committed version hint in memory.
func (w *sourceWorker) noteVersion(id string, result importResult) {
	if result.mismatch {
		delete(w.versions, id)
		return
	}
	w.versions[id] = result.version
}

// withdraw removes the hint and the read receipt after a failed or deferred
// read, or once the source no longer has the session, so readers never trust
// content the import could not verify.
func (w *sourceWorker) withdraw(ctx context.Context, id string) {
	delete(w.versions, id)
	// Best effort, like forget: the failure that led here is already reported.
	_, _ = w.module.deps.Store.ApplySource(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: id},
		statev2.SourceUpdate{State: map[string]string{versionKey + id: "", readReadyPrefix + id: ""}})
}

func (w *sourceWorker) approveRead(ctx context.Context, id, version string, tickets ...uint64) error {
	if err := w.module.deps.Store.SetSourceState(ctx, w.instance.ID, readReadyPrefix+id, version); err != nil {
		return err
	}
	w.readReady(id, tickets...)
	return nil
}

// readReady publishes a receipt that is already committed.
func (w *sourceWorker) readReady(id string, tickets ...uint64) {
	w.module.publishReceipt(w.source.AdapterID, id, tickets...)
	w.completePriority(id)
}
