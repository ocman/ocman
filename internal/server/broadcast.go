package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/httpx"
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
	key   string // coalesceKey, computed once per broadcast
	// change is the decoded ocman.session.changed payload (decoded once per
	// broadcast and shared read-only), so merging parked changes under the
	// subscriber lock is map work only. A merge clears data; batch
	// re-encodes it after the lock is released.
	change *sessionChange
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
	// publishMu serializes whole publications, so every subscriber sees
	// events in the same order (full-state snapshots are applied as-is).
	// mu guards only membership.
	publishMu sync.Mutex
	mu        sync.Mutex
	subs      map[*broadcastSub]struct{}
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
		Platform  string `json:"platform"`
		RunID     string `json:"runId"`
	}
	if err := json.Unmarshal(data, &p); err == nil && p.SessionID != "" {
		if p.Platform != "" {
			return event + "\x00" + p.Platform + "\x00" + p.SessionID
		}
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
//
// The event is parsed once, before taking any lock. The membership lock
// covers only the snapshot: deliver tolerates a subscriber that
// unsubscribes meanwhile, and subscribing never waits on a fan-out.
func (h *broadcastHub) broadcast(event string, data []byte) {
	ev := broadcastEvent{event: event, data: data}
	if coalescingEvents[event] {
		ev.key = coalesceKey(event, data)
		if event == "ocman.session.changed" {
			ev.change = decodeSessionChange(data)
		}
	}
	h.publishMu.Lock()
	defer h.publishMu.Unlock()
	h.mu.Lock()
	subs := make([]*broadcastSub, 0, len(h.subs))
	for sub := range h.subs {
		subs = append(subs, sub)
	}
	h.mu.Unlock()
	for _, sub := range subs {
		sub.deliver(ev)
	}
}

// deliver queues ev on the subscriber's buffer, or parks it when it is a
// coalescing event that doesn't fit. Once a key is parked, later events
// for it are parked too until the writer takes them: sending one through
// the buffer could overtake the older parked payload. The whole decision
// runs under s.mu, and the writer takes buffer and parked events as one
// batch under the same lock (takeBatch), so each key's events reach the
// client in publish order. ev.key must already be set for coalescing events.
func (s *broadcastSub) deliver(ev broadcastEvent) {
	coalescing := coalescingEvents[ev.event]
	key := ev.key
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
		ev = mergeSessionChanges(old, ev)
	}
	s.pending[key] = ev
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// takeBatch atomically removes everything buffered, then everything
// parked, in that order. Buffered events for a key are always older than
// its parked one, and no producer can interleave while the batch is taken.
// The caller writes the batch outside the lock.
func (s *broadcastSub) takeBatch() []broadcastEvent {
	s.mu.Lock()
	var batch []broadcastEvent
	for n := len(s.ch); n > 0; n-- {
		batch = append(batch, <-s.ch)
	}
	for k, ev := range s.pending {
		batch = append(batch, ev)
		delete(s.pending, k)
	}
	s.mu.Unlock()
	return encodeAll(batch)
}

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

// broadcastGlobalEvent is the Server-level helper used by other code
// paths to push an event to every connected /api/events client. No-op
// if the hub hasn't been initialised (e.g. zero-value Server in tests).
func (s *Server) broadcastGlobalEvent(event string, data []byte) {
	if s == nil || s.broadcastHub == nil {
		return
	}
	s.broadcastHub.broadcast(event, data)
	s.observeHistoryChange(event, data)
}

// broadcastPermissionResolved broadcasts that a permission prompt is no
// longer pending (auto-approved, or answered via the TUI / another tab),
// so cross-page prompt toasts for the session clear immediately. reason
// is a short tag for diagnostics ("auto-approved", "replied").
func (s *Server) broadcastPermissionResolved(platform, sessionID, permissionID, reason string) {
	s.resolvePermissionInbox(context.Background(), platform, sessionID, permissionID)
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"platform":     platform,
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
func (s *Server) broadcastQuestionResolved(platform, sessionID, requestID, reason string) {
	if sessionID == "" {
		return
	}
	payload, err := json.Marshal(map[string]interface{}{
		"platform":  platform,
		"sessionID": sessionID,
		"requestId": requestID,
		"reason":    reason,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.question.resolved", payload)
}

// sseKeepaliveInterval is how often browser streams carry a named
// keepalive: it keeps idle proxies open and lets the client detect a
// half-open stream (frontend KEEPALIVE_TIMEOUT_MS). A var for tests.
var sseKeepaliveInterval = 15 * time.Second

// sseWriteTimeout bounds each write to a browser stream, so a client that
// stopped reading is dropped (and unsubscribed) instead of pinning its
// goroutine until TCP gives up. A var for tests.
var sseWriteTimeout = 30 * time.Second

// writeSSE writes one chunk under a fresh write deadline and flushes it.
// Writers without deadline support (test recorders) write without one.
func writeSSE(w http.ResponseWriter, rc *http.ResponseController, chunk []byte) error {
	if err := rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return err
	}
	if _, err := w.Write(chunk); err != nil {
		return err
	}
	return rc.Flush()
}

func sseEventChunk(ev broadcastEvent) []byte {
	return fmt.Appendf(nil, "event: %s\ndata: %s\n\n", ev.event, ev.data)
}

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

	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	rc := http.NewResponseController(w)

	sub, unsub := s.broadcastHub.subscribe()
	defer unsub()

	// Initial flush so the client's EventSource transitions to the
	// open state immediately rather than waiting for the first event.
	w.WriteHeader(http.StatusOK)
	if writeSSE(w, rc, nil) != nil {
		return
	}

	keepalive := time.NewTicker(sseKeepaliveInterval)
	defer keepalive.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-keepalive.C:
			if writeSSE(w, rc, []byte(httpx.SSEKeepalive)) != nil {
				return
			}
		case <-sub.wake:
			// Coalesced last-write-wins events that didn't fit the buffer —
			// flush the freshest snapshot per key so state (e.g. the queue
			// list) is never lost under load. Buffered events are older than
			// any parked one for the same key, so write them first.
			for _, ev := range sub.takeBatch() {
				if writeSSE(w, rc, sseEventChunk(ev)) != nil {
					return
				}
			}
		case ev, open := <-sub.ch:
			if !open || writeSSE(w, rc, sseEventChunk(ev)) != nil {
				return
			}
		}
	}
}
