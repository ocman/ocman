package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
)

// --- Global broadcast hub ---
//
// The per-session SSE registry (sseSessions) only reaches the single
// connection that's currently viewing a session. Some events need to
// reach *every* connected client regardless of which page they're on —
// notably "this permission was resolved" so cross-page prompt toasts
// can clear the moment the LLM judge auto-approves, instead of waiting
// for the next /api/sessions/notify poll.
//
// broadcastHub is a tiny fan-out: clients subscribe by registering a
// buffered channel; broadcast() pushes a serialized event to every
// subscriber. Slow/blocked subscribers are skipped (non-blocking send)
// so one stuck client can't stall the auto-approve goroutine.

// broadcastEvent is a single named event delivered to every global
// subscriber. Mirrors the SSE wire shape (event name + JSON data).
type broadcastEvent struct {
	event string
	data  []byte
}

// broadcastHub fans out events to all connected /api/events clients.
type broadcastHub struct {
	mu   sync.Mutex
	subs map[chan broadcastEvent]struct{}
}

func newBroadcastHub() *broadcastHub {
	return &broadcastHub{subs: make(map[chan broadcastEvent]struct{})}
}

// subscribe registers a new subscriber and returns its channel plus an
// unsubscribe func. The channel is buffered so a brief consumer stall
// doesn't immediately drop events.
func (h *broadcastHub) subscribe() (<-chan broadcastEvent, func()) {
	ch := make(chan broadcastEvent, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, ch)
			h.mu.Unlock()
			close(ch)
		})
	}
	return ch, unsub
}

// broadcast delivers event to every current subscriber. The send is
// non-blocking: if a subscriber's buffer is full it is skipped rather
// than blocking the caller (the client will fall back to the notify
// poll). Safe to call from any goroutine.
func (h *broadcastHub) broadcast(event string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- broadcastEvent{event: event, data: data}:
		default:
			// Subscriber buffer full — drop. The 10s notify poll is
			// the safety net for any missed broadcast.
		}
	}
}

// subscriberCount reports how many clients are currently connected.
// Used by tests.
func (h *broadcastHub) subscriberCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

// broadcastGlobalEvent is the Server-level helper used by other code
// paths to push an event to every connected /api/events client. No-op
// if the hub hasn't been initialised (e.g. zero-value Server in tests).
func (s *Server) broadcastGlobalEvent(event string, data []byte) {
	if s == nil || s.broadcastHub == nil {
		return
	}
	s.broadcastHub.broadcast(event, data)
}

// broadcastPermissionResolved broadcasts that a permission prompt is no
// longer pending (auto-approved, or answered via the TUI / another tab),
// so cross-page prompt toasts for the session clear immediately. reason
// is a short tag for diagnostics ("auto-approved", "replied").
func (s *Server) broadcastPermissionResolved(sessionID, permissionID, reason string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID":    sessionID,
		"permissionId": permissionID,
		"reason":       reason,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.permission.resolved", payload)
}

// broadcastQuestionResolved broadcasts that a question prompt is no
// longer pending (answered or rejected), so cross-page prompt toasts
// for the session clear immediately.
func (s *Server) broadcastQuestionResolved(sessionID, requestID, reason string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": sessionID,
		"requestId": requestID,
		"reason":    reason,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.question.resolved", payload)
}

// broadcastSessionIdle broadcasts that a session went idle (the agent
// finished a turn). Lets the bell / favicon / completed-but-unseen
// indicators surface promptly instead of waiting for the notify poll.
func (s *Server) broadcastSessionIdle(sessionID string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": sessionID,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.session.idle", payload)
}

// broadcastSessionChanged broadcasts that a session was created or
// changed upstream (OpenCode session.updated), so the session list
// refreshes immediately instead of waiting for the next poll. This is
// what makes a freshly-created session appear near-instantly.
func (s *Server) broadcastSessionChanged(sessionID string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": sessionID,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.session.changed", payload)
}

// broadcastLoopUpdated broadcasts that an agent loop's state changed
// (iteration advance, transition, budget crossing), so the Loops view
// updates live without busy-polling (AD-10).
func (s *Server) broadcastLoopUpdated(loopID string) {
	if loopID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"loopId": loopID,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("loop.updated", payload)
}

// globalEventsKeepaliveInterval is how often we send an SSE comment to
// keep the connection (and any intermediary proxy) alive while idle.
const globalEventsKeepaliveInterval = 25 * time.Second

// handleGlobalEvents serves GET /api/events: an app-wide SSE stream
// that fans out broadcast events to every connected client regardless
// of which page they're on. Used by the frontend to clear cross-page
// prompt toasts the moment a permission is resolved (e.g. auto-approved
// by the LLM judge) instead of waiting for the next notify poll.
func (s *Server) handleGlobalEvents(w http.ResponseWriter, r *http.Request) {
	if s.broadcastHub == nil {
		http.Error(w, "broadcast hub unavailable", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ch, unsub := s.broadcastHub.subscribe()
	defer unsub()

	// Initial flush so the client's EventSource transitions to the
	// open state immediately rather than waiting for the first event.
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	keepalive := time.NewTicker(globalEventsKeepaliveInterval)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			// SSE comment line — ignored by EventSource but keeps the
			// connection warm through idle-timeout proxies.
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			autoapprove.WriteSSEEvent(w, flusher.Flush, ev.event, ev.data)
		}
	}
}
