package conversationingest

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

// repairAfter is how long a waiting read trusts the queued change before
// asking for that one session to be reread, in case its event was missed.
const repairAfter = time.Second

// Await wakes the session's source worker and waits, bounded by ctx, until
// the session's read receipt covers version. It reads only one bookkeeping
// row per receipt change; it never reads platform content itself.
func (m *Module) Await(ctx context.Context, adapterID, sessionID, version string) bool {
	instanceID := m.instanceFor(adapterID)
	if instanceID == "" && !m.hasSource(adapterID) {
		return false
	}
	repair := time.NewTimer(repairAfter)
	defer repair.Stop()
	m.wakeSource(adapterID)
	for {
		changed := m.receiptChanges()
		if instanceID == "" {
			instanceID = m.instanceFor(adapterID)
		}
		if instanceID != "" {
			receipt, err := m.deps.Store.SourceValue(ctx, instanceID, "read-ready/"+sessionID)
			if err == nil && receipt != "" && db.VersionCovers(receipt, version) {
				return true
			}
		}
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		case <-repair.C:
			m.Notify(adapterID, sessionID)
		}
	}
}

// receiptChanges returns a channel closed by the next read approval.
func (m *Module) receiptChanges() <-chan struct{} {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	if m.receiptCh == nil {
		m.receiptCh = make(chan struct{})
	}
	return m.receiptCh
}

func (m *Module) publishReceipt() {
	m.receiptMu.Lock()
	defer m.receiptMu.Unlock()
	if m.receiptCh != nil {
		close(m.receiptCh)
		m.receiptCh = nil
	}
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

func (m *Module) hasSource(adapterID string) bool {
	for _, w := range m.workers {
		if w.source.AdapterID == adapterID {
			return true
		}
	}
	return false
}

func (m *Module) wakeSource(adapterID string) {
	for _, w := range m.workers {
		if w.source.AdapterID == adapterID {
			w.signal()
		}
	}
}
