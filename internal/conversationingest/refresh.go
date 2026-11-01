package conversationingest

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/db"
)

func (m *Module) readTicket(adapterID, id string) uint64 {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	return m.readRequests[[2]string{adapterID, id}]
}

// RefreshStored waits for this session's next successful source validation.
// Unrelated receipts cannot satisfy the wait, and duplicate source versions
// still acknowledge a checked repair without inventing a history revision.
func (m *Module) RefreshStored(ctx context.Context, adapterID, id string) error {
	if m == nil || m.deps.Store == nil || adapterID == "" || id == "" {
		return fmt.Errorf("history refresh requires an ingestion source and session")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	key := [2]string{adapterID, id}
	ticket := m.nextReadTicket(adapterID, id)
	m.requestRead(adapterID, id)
	for {
		changed := m.receiptChanges()
		m.receiptMu.Lock()
		validated := m.readValidated[key]
		missing := m.readMissing[key]
		m.receiptMu.Unlock()
		if missing >= ticket {
			return db.ErrSessionNotFound
		}
		if validated >= ticket {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

func (m *Module) nextReadTicket(adapterID, id string) uint64 {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	if m.readRequests == nil {
		m.readRequests = map[[2]string]uint64{}
	}
	key := [2]string{adapterID, id}
	m.readRequests[key]++
	return m.readRequests[key]
}
