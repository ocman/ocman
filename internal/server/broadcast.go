package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
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

// coalescingEvents are last-write-wins full-state snapshots: a newer
// payload fully supersedes an older one for the same key, so they must
// never be dropped on a full buffer (unlike edge/notification events,
// where the notify poll is an acceptable backstop). When the buffer is
// full, the hub stores the latest payload per key on the subscriber and
// the SSE writer flushes it — guaranteeing the freshest state reaches the
// client without ever blocking a producer. The key is derived by
// coalesceKey (event + session id).
var coalescingEvents = map[string]bool{
	"ocman.queue.updated":  true,
	"workflow.run.updated": true,
}

// broadcastSub is one connected /api/events client. Non-coalescing events
// go through the bounded, lossy channel (ch). Coalescing events that don't
// fit are parked in pending (latest-wins per key) and the writer is woken
// via wake.
type broadcastSub struct {
	ch   chan broadcastEvent
	wake chan struct{} // buffered(1) signal: pending has entries to flush

	mu      sync.Mutex
	pending map[string]broadcastEvent // key -> latest coalesced event
	closed  bool
}

// broadcastHub fans out events to all connected /api/events clients.
type broadcastHub struct {
	mu   sync.Mutex
	subs map[*broadcastSub]struct{}
}

func newBroadcastHub() *broadcastHub {
	return &broadcastHub{subs: make(map[*broadcastSub]struct{})}
}

// coalesceKey identifies a last-write-wins stream for an event: same event
// + same session collapses to one pending entry. Falls back to the event
// name when no session id is present.
func coalesceKey(event string, data []byte) string {
	var p struct {
		SessionID string `json:"sessionID"`
		RunID     string `json:"runId"`
	}
	if err := json.Unmarshal(data, &p); err == nil && p.SessionID != "" {
		return event + "\x00" + p.SessionID
	}
	if p.RunID != "" {
		return event + "\x00" + p.RunID
	}
	return event
}

// subscribe registers a new subscriber and returns it plus an unsubscribe
// func. The channel is buffered so a brief consumer stall doesn't
// immediately drop events.
func (h *broadcastHub) subscribe() (*broadcastSub, func()) {
	sub := &broadcastSub{
		ch:      make(chan broadcastEvent, 16),
		wake:    make(chan struct{}, 1),
		pending: make(map[string]broadcastEvent),
	}
	h.mu.Lock()
	h.subs[sub] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs, sub)
			h.mu.Unlock()
			sub.mu.Lock()
			sub.closed = true
			sub.mu.Unlock()
			close(sub.ch)
		})
	}
	return sub, unsub
}

// broadcast delivers event to every current subscriber. Never blocks a
// producer. Non-coalescing events are dropped on a full buffer (the
// notify poll is the backstop); coalescing events are instead parked as
// the latest-wins pending state for the subscriber and the writer is
// woken to flush them, so the freshest full-state snapshot is never lost.
func (h *broadcastHub) broadcast(event string, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for sub := range h.subs {
		ev := broadcastEvent{event: event, data: data}
		select {
		case sub.ch <- ev:
		default:
			if coalescingEvents[event] {
				sub.park(coalesceKey(event, data), ev)
			}
			// else: non-coalescing edge event — drop (notify poll backstop).
		}
	}
}

// park stores the latest coalesced event for a key and signals the
// writer. A newer payload overwrites an older pending one (last-write-
// wins), so a burst collapses to a single freshest snapshot.
func (s *broadcastSub) park(key string, ev broadcastEvent) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.pending[key] = ev
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// drainPending returns and clears the subscriber's pending coalesced
// events. Called by the SSE writer when woken.
func (s *broadcastSub) drainPending() []broadcastEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) == 0 {
		return nil
	}
	out := make([]broadcastEvent, 0, len(s.pending))
	for k, ev := range s.pending {
		out = append(out, ev)
		delete(s.pending, k)
	}
	return out
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

// broadcastSessionCreated broadcasts a freshly-created (or moved)
// session with a provisional list row the frontend can insert
// immediately, ahead of the authoritative refetch the same event
// triggers. The row is built from what the service knew without extra
// I/O (id, platform, directory, title); missing fields default to a
// harmless "waiting" row that the refetch overwrites.
func (s *Server) broadcastSessionCreated(info sessionsvc.CreatedSession) {
	if info.ID == "" {
		return
	}
	now := time.Now().UnixMilli()
	session := db.Session{
		ID:          info.ID,
		Platform:    info.Platform,
		Directory:   info.Directory,
		Title:       info.Title,
		TimeCreated: now,
		TimeUpdated: now,
		Status:      db.StatusWaiting,
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": info.ID,
		"session":   session,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.session.changed", payload)
}

func (s *Server) broadcastWorkflowRunUpdated(runID string) {
	if runID == "" {
		return
	}
	payload, err := json.Marshal(map[string]string{"runId": runID})
	if err == nil {
		s.broadcastGlobalEvent("workflow.run.updated", payload)
	}
}

func (s *Server) broadcastWorkflowTriggerUpdated() {
	s.broadcastGlobalEvent("workflow.trigger.updated", []byte(`{}`))
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

	sub, unsub := s.broadcastHub.subscribe()
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
		case <-sub.wake:
			// Coalesced last-write-wins events that didn't fit the buffer —
			// flush the freshest snapshot per key so state (e.g. the queue
			// list) is never lost under load.
			for _, ev := range sub.drainPending() {
				autoapprove.WriteSSEEvent(w, flusher.Flush, ev.event, ev.data)
			}
		case ev, open := <-sub.ch:
			if !open {
				return
			}
			autoapprove.WriteSSEEvent(w, flusher.Flush, ev.event, ev.data)
		}
	}
}
