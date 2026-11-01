package conversationingest

import (
	"context"
	"errors"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/statev2"
)

func missingSession(err error) bool {
	return errors.Is(err, platforms.ErrNotFound) || errors.Is(err, db.ErrSessionNotFound)
}

// Not-found can race creation or source replacement. Only a complete inventory
// from this worker's bound source proves that the queued read is obsolete.
func (w *sourceWorker) reconcileMissing(ctx context.Context, id string, ticket uint64) (bool, error) {
	stored, err := w.module.deps.Store.SessionState(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: id})
	if err != nil {
		return false, err
	}
	readCtx, cancel := context.WithTimeout(ctx, w.module.deps.ReadTimeout)
	defer cancel()
	entries, deferred, err := readInventory(readCtx, w.source.Reader)
	if err != nil || deferred > 0 {
		return false, err
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.ID == "" || seen[entry.ID] {
			return false, errors.New("history inventory contains an empty or duplicate identity")
		}
		seen[entry.ID] = true
	}
	if seen[id] {
		return false, nil
	}
	return true, w.resolveMissingState(ctx, id, ticket, stored)
}

func (w *sourceWorker) resolveMissing(ctx context.Context, id string, ticket uint64) error {
	stored, err := w.module.deps.Store.SessionState(ctx, statev2.Key{InstanceID: w.instance.ID, SessionID: id})
	if err != nil {
		return err
	}
	return w.resolveMissingState(ctx, id, ticket, stored)
}

func (w *sourceWorker) resolveMissingState(ctx context.Context, id string, ticket uint64, stored statev2.Session) error {
	if stored.Revision > 0 && !stored.SourceDeleted {
		if err := w.markDeleted(ctx, stored); err != nil {
			return err
		}
	}
	w.forget(ctx, id)
	delete(w.failing, id)
	w.publishMissing(id, ticket)
	return nil
}

// Missing is a terminal result for the captured refresh ticket, not a successful
// content validation. Newer requests cannot be satisfied by an older inventory.
func (w *sourceWorker) publishMissing(id string, ticket uint64) {
	m := w.module
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	key := [2]string{w.source.AdapterID, id}
	if ticket > 0 {
		if m.readMissing == nil {
			m.readMissing = map[[2]string]uint64{}
		}
		m.readMissing[key] = max(m.readMissing[key], ticket)
	}
	if m.readRequests[key] <= ticket {
		w.mu.Lock()
		delete(w.priority, id)
		w.mu.Unlock()
	}
	if m.receiptCh != nil {
		close(m.receiptCh)
		m.receiptCh = nil
	}
}

func (m *Module) readTickets(adapterID string) map[string]uint64 {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	tickets := map[string]uint64{}
	for key, ticket := range m.readRequests {
		if key[0] == adapterID {
			tickets[key[1]] = ticket
		}
	}
	return tickets
}

func (w *sourceWorker) resolveAbsent(ctx context.Context, present map[string]bool, tickets map[string]uint64, stored map[string]statev2.Session) {
	for id, ticket := range tickets {
		// Live retained rows are handled by the pass's revision-guarded
		// deletions. Do not repeat them against the old captured revision.
		if !present[id] && (stored[id].Revision == 0 || stored[id].SourceDeleted) {
			w.forget(ctx, id)
			delete(w.failing, id)
			w.publishMissing(id, ticket)
		}
	}
}
