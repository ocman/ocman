package platformsettings

// RequestRecoveryReload shares the existing admission drain and authoritative
// idle checks, without changing saved platform selection. Settings writes cannot
// cancel a recovery reload that has already been requested.
func (m *Module) RequestRecoveryReload() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.reload == nil {
		return false
	}
	m.recoveryPending = true
	return true
}
