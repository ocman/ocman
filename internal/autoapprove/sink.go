package autoapprove

import (
	"fmt"
	"io"
)

// WriteSSEEvent writes a single named SSE event to w and calls flush if
// non-nil. This is used by backgroundAutoApprove to push synthetic
// ocman-originated events (e.g. permission.checking, permission.auto-approved)
// back to connected browser clients through the proxied event stream.
func WriteSSEEvent(w io.Writer, flush func(), eventType string, data []byte) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, string(data))
	if flush != nil {
		flush()
	}
}

// --- Per-session SSE writer registry ---
//
// Active SSE connections register their writer here so non-SSE code paths
// (REST permission listing, prompt resurrection) can push synthetic
// ocman.permission.* events into the same connection.

// RegisterSink creates and records an Sink for sessionID. The
// returned pointer must be passed to UnregisterSink when the
// connection terminates so the sink is closed (any in-flight or
// future writes turn into no-ops, preventing panics on a recycled
// http.ResponseWriter).
//
// If a sink was already registered for the same sessionID (rare —
// second tab on the same session) the previous one is closed; the
// older client will simply stop receiving ocman.* events but its
// proxied OpenCode events continue unaffected.
func (s *Service) RegisterSink(sessionID string, w io.Writer, flush func()) *Sink {
	if s == nil {
		return nil
	}
	sink := &Sink{w: w, flush: flush}
	s.sseSessionsMu.Lock()
	if s.sseSessions == nil {
		s.sseSessions = make(map[string]*Sink)
	}
	prev := s.sseSessions[sessionID]
	s.sseSessions[sessionID] = sink
	s.sseSessionsMu.Unlock()
	if prev != nil {
		prev.close()
	}
	return sink
}

// UnregisterSink closes the sink (so any in-flight or future writes
// become no-ops) and removes it from the registry, but only if it
// still matches the one being closed. This avoids clobbering a newer
// tab's registration when an old SSE connection finally tears down.
func (s *Service) UnregisterSink(sessionID string, sink *Sink) {
	if s == nil || sink == nil {
		return
	}
	s.sseSessionsMu.Lock()
	if cur, ok := s.sseSessions[sessionID]; ok && cur == sink {
		delete(s.sseSessions, sessionID)
	}
	s.sseSessionsMu.Unlock()
	sink.close()
}

// lookupSink returns the registered sink for sessionID, or nil if
// none. The returned pointer is stable — closing it is safe even after
// the registry entry has been removed or replaced.
func (s *Service) lookupSink(sessionID string) *Sink {
	if s == nil {
		return nil
	}
	s.sseSessionsMu.Lock()
	defer s.sseSessionsMu.Unlock()
	return s.sseSessions[sessionID]
}
