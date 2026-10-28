package conversationingest

import (
	"context"

	"github.com/ocman/ocman/internal/db"
)

// Requested reads must not wait behind the entire historical inventory or
// its cooldown. This runs on the same source worker, never concurrently with
// another read of that source. Live reads bypass the background slot.
func (w *sourceWorker) servePriority(ctx context.Context) string {
	if ctx.Err() != nil {
		return ""
	}
	w.mu.Lock()
	var id string
	for candidate := range w.priority {
		id = candidate
		delete(w.priority, candidate)
		break // bound work per background boundary
	}
	more := len(w.priority) > 0
	w.mu.Unlock()
	if more {
		w.signal()
	}
	if id == "" {
		return ""
	}
	previous := w.last
	defer func() { w.last = previous }()
	var err error
	if w.feed != nil {
		err = w.refreshFeedSession(ctx, id, true)
	} else {
		err = w.requireComplete(w.execute(ctx, pass{full: w.explicitReadPending(id), reads: []db.HistoryEntry{{ID: id, Version: w.versions[id]}}}, nil))
	}
	if err != nil {
		w.failing[id] = err.Error()
		w.module.report(w.instance.ID, id, err)
		return ""
	}
	return id
}

func (w *sourceWorker) completePriority(id string) {
	m := w.module
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	key := [2]string{w.source.AdapterID, id}
	if m.readRequests[key] > m.readValidated[key] {
		return // a newer explicit request arrived during the source read
	}
	w.mu.Lock()
	delete(w.priority, id)
	w.mu.Unlock()
}
