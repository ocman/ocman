package conversationingest

import "time"

// LastCheckedAt is the oldest successful source scan in the current import.
// ReadyInstances independently decides whether the set is usable.
func (m *Module) LastCheckedAt() time.Time {
	m.readyMu.Lock()
	defer m.readyMu.Unlock()
	var oldest time.Time
	for _, r := range m.ready {
		if oldest.IsZero() || r.at.Before(oldest) {
			oldest = r.at
		}
	}
	return oldest
}
