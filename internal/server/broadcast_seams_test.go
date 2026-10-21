package server

// drainPending returns and clears the subscriber's pending coalesced
// events. Called by the SSE writer when woken.
func (s *broadcastSub) drainPending() []broadcastEvent {
	s.mu.Lock()
	out := make([]broadcastEvent, 0, len(s.pending))
	for k, ev := range s.pending {
		out = append(out, ev)
		delete(s.pending, k)
	}
	s.mu.Unlock()
	return encodeAll(out)
}

// subscriberCount reports how many clients are currently connected.
// Used by tests.
func (h *broadcastHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}
