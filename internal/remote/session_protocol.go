package remote

func (c *RemoteConn) requiringCanonical(required bool) *RemoteConn {
	c.mu.Lock()
	c.requireCanonical = required
	c.mu.Unlock()
	return c
}

func (c *RemoteConn) acceptsSessionProtocol(version int32) bool {
	c.mu.RLock()
	required := c.requireCanonical
	c.mu.RUnlock()
	return protocolCompatible(version) && (!required || version >= canonicalSessionProtocol)
}

// Publication is independently fenced even for manually supplied or already
// connected transports. A legacy owner cannot acquire host execution authority.
func (m *Manager) canPublishSessionContract(conn *RemoteConn) bool {
	if !m.registry.CanonicalSessionIDs() || conn.ProtocolVersion() >= canonicalSessionProtocol {
		return true
	}
	conn.setHealth(HealthIncompatible)
	return false
}
