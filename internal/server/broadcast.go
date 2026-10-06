package server

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

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

// coalescingEvents are last-write-wins per coalesceKey: a newer payload
// fully supersedes an older one for the same key, so they must never be
// dropped on a full buffer (unlike edge/notification events, where the
// notify poll is an acceptable backstop). When the buffer is full, the
// hub stores the latest payload per key on the subscriber and the SSE
// writer flushes it — guaranteeing the freshest state reaches the client
// without ever blocking a producer. The key is derived by coalesceKey
// (event + session id).
//
// ocman.session.idle / ocman.session.changed are identity-keyed: the
// consumer re-reads state on receipt (useGlobalEvents refetches; the
// provisional session/patch payloads are optimizations ahead of that
// refetch), so keeping only the newest per (event, session) is safe —
// and dropping them stalled queue drain, notifications, and the sidebar
// under bursts (#490).
var coalescingEvents = map[string]bool{
	"ocman.queue.updated":    true,
	"ocman.session.idle":     true,
	"ocman.session.changed":  true,
	"ocman.session.activity": true,
	"ocman.projects.changed": true,
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
		sub.deliver(broadcastEvent{event: event, data: data})
	}
}

// deliver queues ev on the subscriber's buffer, or parks it when it is a
// coalescing event that doesn't fit. Once a key is parked, later events
// for it are parked too until the writer takes them: sending one through
// the buffer could overtake the older parked payload. The whole decision
// runs under s.mu, and the writer takes buffer and parked events as one
// batch under the same lock (takeBatch), so each key's events reach the
// client in publish order.
func (s *broadcastSub) deliver(ev broadcastEvent) {
	coalescing := coalescingEvents[ev.event]
	key := ""
	if coalescing {
		key = coalesceKey(ev.event, ev.data)
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if _, parked := s.pending[key]; !coalescing || !parked {
		select {
		case s.ch <- ev:
			s.mu.Unlock()
			return
		default:
		}
	}
	if !coalescing {
		s.mu.Unlock()
		// Non-coalescing edge event — drop (notify poll backstop).
		// Logged so a systematic backlog is visible instead of
		// presenting as unexplained lag (#490).
		log.WithField("event", ev.event).Debug("global SSE: dropped event for slow subscriber")
		return
	}
	if old, ok := s.pending[key]; ok && ev.event == "ocman.session.changed" {
		ev.data = mergeSessionPatches(old.data, ev.data)
	}
	s.pending[key] = ev
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// mergeSessionPatches folds an older pending session change into a newer
// one for a slow subscriber, so nothing the older one asked for is lost:
// two patches merge field by field (a status patch keeps a pending title),
// and if either side is identity-only the result stays identity-only, since
// that asks the consumer to refetch and a refetch subsumes any patch.
func mergeSessionPatches(older, newer []byte) []byte {
	var o, n map[string]json.RawMessage
	if json.Unmarshal(older, &o) != nil || json.Unmarshal(newer, &n) != nil {
		return newer
	}
	if o["patch"] == nil || n["patch"] == nil {
		delete(n, "patch")
	} else {
		var op, np map[string]json.RawMessage
		if json.Unmarshal(o["patch"], &op) != nil || json.Unmarshal(n["patch"], &np) != nil {
			return newer
		}
		for k, v := range np {
			op[k] = v
		}
		merged, err := json.Marshal(op)
		if err != nil {
			return newer
		}
		n["patch"] = merged
	}
	out, err := json.Marshal(n)
	if err != nil {
		return newer
	}
	return out
}

// takeBatch atomically removes everything buffered, then everything
// parked, in that order. Buffered events for a key are always older than
// its parked one, and no producer can interleave while the batch is taken.
// The caller writes the batch outside the lock.
func (s *broadcastSub) takeBatch() []broadcastEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	var batch []broadcastEvent
	for n := len(s.ch); n > 0; n-- {
		batch = append(batch, <-s.ch)
	}
	for k, ev := range s.pending {
		batch = append(batch, ev)
		delete(s.pending, k)
	}
	return batch
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
	s.resolvePermissionInbox(context.Background(), "opencode", sessionID, permissionID)
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
			// list) is never lost under load. Buffered events are older than
			// any parked one for the same key, so write them first.
			for _, ev := range sub.takeBatch() {
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
