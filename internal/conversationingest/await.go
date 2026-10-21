package conversationingest

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/statev2"
)

// receiptChanges returns a channel closed by the next read approval.
func (m *Module) receiptChanges() <-chan struct{} {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	if m.receiptCh == nil {
		m.receiptCh = make(chan struct{})
	}
	return m.receiptCh
}

func (m *Module) publishReceipt(adapterID, id string, tickets ...uint64) {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	key := [2]string{adapterID, id}
	ticket := m.readRequests[key]
	if len(tickets) > 0 {
		ticket = tickets[0]
	}
	if ticket > 0 {
		if m.readValidated == nil {
			m.readValidated = map[[2]string]uint64{}
		}
		m.readValidated[key] = max(m.readValidated[key], ticket)
	}
	if m.receiptCh != nil {
		close(m.receiptCh)
		m.receiptCh = nil
	}
}

// AwaitStored waits for the first displayable persisted snapshot. It never
// probes native content itself; the source worker owns all ingestion reads.
func (m *Module) AwaitStored(ctx context.Context, adapterID, id string) bool {
	if m == nil || m.deps.Store == nil || id == "" {
		return false
	}
	m.requestRead(adapterID, id)
	for ctx.Err() == nil {
		changed := m.receiptChanges()
		if instance := m.instanceFor(adapterID); instance != "" {
			s, purged, err := m.deps.Store.SessionState(ctx, statev2.Key{InstanceID: instance, SessionID: id})
			if purged {
				return false
			}
			if err == nil && s.Revision > 0 && statev2.ReadableConversationFormat(s.ReadFormat) {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
	return false
}

func (m *Module) instanceFor(adapterID string) string {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	return m.instances[adapterID]
}

func (m *Module) setInstance(adapterID, instanceID string) {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	if m.instances == nil {
		m.instances = map[string]string{}
	}
	m.instances[adapterID] = instanceID
}
