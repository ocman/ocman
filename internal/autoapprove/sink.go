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

// RegisterSink creates and records a Sink for sessionID. The
// returned pointer must be passed to UnregisterSink when the
// connection terminates so the sink is closed (any in-flight or
// future writes turn into no-ops, preventing panics on a recycled
// http.ResponseWriter).
func (s *Service) RegisterSink(sessionID string, w io.Writer, flush func()) *Sink {
	if s == nil {
		return nil
	}
	sink := &Sink{w: w, flush: flush}
	s.sseSessionsMu.Lock()
	if s.sseSessions == nil {
		s.sseSessions = make(map[string]map[*Sink]struct{})
	}
	if s.sseSessions[sessionID] == nil {
		s.sseSessions[sessionID] = make(map[*Sink]struct{})
	}
	s.sseSessions[sessionID][sink] = struct{}{}
	s.sseSessionsMu.Unlock()
	return sink
}

// UnregisterSink closes the sink (so any in-flight or future writes
// become no-ops) and removes only it from the registry.
func (s *Service) UnregisterSink(sessionID string, sink *Sink) {
	if s == nil || sink == nil {
		return
	}
	s.sseSessionsMu.Lock()
	if sinks := s.sseSessions[sessionID]; sinks != nil {
		delete(sinks, sink)
	}
	if len(s.sseSessions[sessionID]) == 0 {
		delete(s.sseSessions, sessionID)
	}
	s.sseSessionsMu.Unlock()
	sink.close()
}

// lookupSinks returns a snapshot of the registered sinks for sessionID.
// Each pointer remains safe to write after concurrent unregister because
// Sink.close and Sink.write use the same lock.
func (s *Service) lookupSinks(sessionID string) []*Sink {
	if s == nil {
		return nil
	}
	s.sseSessionsMu.Lock()
	defer s.sseSessionsMu.Unlock()
	sinks := make([]*Sink, 0, len(s.sseSessions[sessionID]))
	for sink := range s.sseSessions[sessionID] {
		sinks = append(sinks, sink)
	}
	return sinks
}
