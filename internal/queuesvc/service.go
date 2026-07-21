// Package queuesvc owns the follow-up message queue: prompts a user
// submits while a session is mid-turn are appended here and drained one
// at a time on each session.idle edge. The queue lives server-side (in
// state.db) so it is shared across every connected client and survives a
// client moving machines — #58.
//
// Design (race-free by construction):
//   - Enqueue is unconditional: SendMessage always appends. There is no
//     "is it busy?" check at enqueue time, so there is no check-then-act
//     race.
//   - Flush is the only authoritative gate. It drains the queue head
//     while the session is idle and stops the moment it goes busy. Sends
//     are serialized per-session by a lock, so a Flush triggered by
//     enqueue and one triggered by a session.idle event can never
//     double-send the same head.
package queuesvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Store is the consumer-side subset of *state.DB the queue needs.
type Store interface {
	EnqueueMessage(m state.QueuedMessage) error
	CountQueuedMessages(platform, sessionID string) (int, error)
	HeadQueuedMessage(platform, sessionID string) (*state.QueuedMessage, error)
	ListQueuedMessages(platform, sessionID string) ([]state.QueuedMessage, error)
	DeleteQueuedMessage(id string) (bool, error)
	MoveQueuedMessage(id string, direction int) (bool, error)
	GetQueuedMessageSession(id string) (platform, sessionID string, ok bool, err error)
	SessionsWithQueuedMessages() ([]state.QueuedSession, error)
}

// Sender forwards a queued message to the owning platform. The server
// wires this to sessionsvc's direct-send path (bypassing the queue so we
// don't re-enqueue what we're draining).
type Sender interface {
	SendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error
}

// StatusInferer reports whether a session's turn is still running. Used
// only at flush time (the authoritative gate).
type StatusInferer interface {
	TurnRunning(ctx context.Context, platform, sessionID string) (running, ok bool)
}

// Service manages the follow-up queue.
type Service struct {
	store  Store
	sender Sender
	status StatusInferer
	notify func(sessionID string) // optional; broadcast queue.updated

	// One lock per session serializes flush drains so an enqueue-driven
	// flush and an idle-driven flush cannot pop the same head twice.
	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// drainGuards[sessionID] records the source message visible before a queued send
	// for that session and we have NOT yet seen a real session.idle edge.
	// It gates the enqueue fast-path so a burst of enqueues can't chain
	// multiple sends into one turn just because the status poll blips to
	// idle (the last message being a user message reads as idle). A genuine
	// session.idle or a newer completed assistant message clears it. Guarded by mu.
	drainGuards    map[string]drainGuard
	nextGeneration uint64
}

type completionInferer interface {
	LatestMessageState(ctx context.Context, platform, sessionID string) (messageID string, createdAt int64, running, completed, ok bool)
}

type drainGuard struct {
	generation uint64
	messageID  string
	createdAt  int64
}

// New builds a queue service. notify may be nil.
func New(store Store, sender Sender, status StatusInferer, notify func(sessionID string)) *Service {
	return &Service{
		store:       store,
		sender:      sender,
		status:      status,
		notify:      notify,
		locks:       map[string]*sync.Mutex{},
		drainGuards: map[string]drainGuard{},
	}
}

func (s *Service) markDrained(sessionID, messageID string, createdAt int64) {
	s.mu.Lock()
	s.nextGeneration++
	s.drainGuards[sessionID] = drainGuard{generation: s.nextGeneration, messageID: messageID, createdAt: createdAt}
	s.mu.Unlock()
}

func (s *Service) currentDrainGuard(sessionID string) (drainGuard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	guard, ok := s.drainGuards[sessionID]
	return guard, ok
}

func (s *Service) clearDrainedSinceIdle(sessionID string) {
	s.mu.Lock()
	delete(s.drainGuards, sessionID)
	s.mu.Unlock()
}

func (s *Service) lockFor(sessionID string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.locks[sessionID]
	if !ok {
		m = &sync.Mutex{}
		s.locks[sessionID] = m
	}
	return m
}

// ErrEmptyMessage is returned by Enqueue when a message has neither text
// nor images. Transports map it to a 400 (same shape sessionsvc uses).
var ErrEmptyMessage = errors.New("message or images required")

// Enqueue appends a message to the session's queue.
//
// forceQueue is the client's authoritative "the agent is mid-turn" signal
// (derived from the live SSE stream). When true, the message is HELD — it
// is never drained here and waits for the next session.idle edge. This is
// the fix for #58: the server's own status inference reads the lagging DB
// and can wrongly report idle mid-turn, which would send the message
// immediately instead of queueing it.
//
// When forceQueue is false (an idle send) the fast path may drain it now:
// only if it is the sole queued message AND nothing has drained since the
// last idle edge. That "first item only" rule keeps a busy session's
// queue intact even if the status poll blips to idle. A genuine
// session.idle (via Flush) drains a backlog.
func (s *Service) Enqueue(ctx context.Context, platformID string, forceQueue bool, req platforms.SendMessageRequest) error {
	return s.enqueue(ctx, newID(), false, platformID, forceQueue, req)
}

// EnqueueOnce durably queues a held message with a stable ID. Repeating the
// same call is a no-op, allowing completion delivery to recover after restart.
func (s *Service) EnqueueOnce(ctx context.Context, id, platformID string, req platforms.SendMessageRequest) error {
	return s.enqueue(ctx, id, true, platformID, true, req)
}

func (s *Service) enqueue(ctx context.Context, id string, once bool, platformID string, forceQueue bool, req platforms.SendMessageRequest) error {
	if req.Message == "" && len(req.Images) == 0 {
		return ErrEmptyMessage
	}
	m := state.QueuedMessage{
		ID:         id,
		Platform:   platformID,
		SessionID:  req.SessionID,
		Text:       req.Message,
		ImagesJSON: encodeImages(req.Images),
		Model:      req.Model,
		Agent:      req.Agent,
		Reasoning:  req.Reasoning,
		CreatedAt:  nowMillis(),
	}

	lock := s.lockFor(req.SessionID)
	lock.Lock()
	defer lock.Unlock()
	if once {
		queuedPlatform, queuedSession, ok, err := s.store.GetQueuedMessageSession(id)
		if err != nil {
			return err
		}
		if ok {
			if queuedPlatform != platformID || queuedSession != req.SessionID {
				return fmt.Errorf("queued message id %q belongs to another session", id)
			}
			return nil
		}
	}

	existing, err := s.store.CountQueuedMessages(platformID, req.SessionID)
	if err != nil {
		return err
	}
	if err := s.store.EnqueueMessage(m); err != nil {
		return err
	}
	// The client says the turn is running: hold the message. Do NOT mark
	// drained here — nothing has been sent yet. Marking drained would
	// disarm the Sweep backstop, so a forceQueue message whose
	// session.idle edge never arrives (watcher disconnected, edge not
	// emitted) would be stranded forever with no send and no idle edge to
	// clear the guard. The busy gate in drainHead (trustIdle=false) is
	// what keeps Sweep from sending into a still-running turn; the guard
	// is only set once a message is actually sent (in drainHead).
	if forceQueue {
		s.fireNotify(req.SessionID)
		return nil
	}
	s.fireNotify(req.SessionID)

	// Idle send fast path — see doc comment.
	if _, guarded := s.currentDrainGuard(req.SessionID); existing == 0 && !guarded {
		s.drainHead(ctx, platformID, req.SessionID, false)
	}
	return nil
}

// Flush sends the single oldest queued message for a session, provided
// the session is idle. It is a no-op when the session is busy or the
// queue is empty. Exactly one message is sent per call: that send starts
// a new turn, and the *next* session.idle edge flushes the next message
// (one follow-up per turn, matching the feature's intent). On a send
// error the message stays at the head so a later idle edge retries it.
//
// Serialized per-session by a lock so an enqueue-driven flush and an
// idle-driven flush can never send the same head twice.
//
// platformID may be empty when driven by a session.idle event that only
// knows the session id; the head row carries the authoritative platform.
func (s *Service) Flush(ctx context.Context, platformID, sessionID string) {
	lock := s.lockFor(sessionID)
	lock.Lock()
	defer lock.Unlock()
	// A real idle edge re-arms the enqueue fast-path: the previous turn
	// has genuinely finished, so the next drained message starts a fresh
	// turn.
	s.clearDrainedSinceIdle(sessionID)
	// trustIdle=true: session.idle IS the authoritative turn-finished
	// signal. Do NOT re-check the inferred status here — it's derived from
	// the last assistant message's finish field, which lags the SSE edge,
	// so it can still read "busy" at this instant. Consulting it would
	// swallow the drain and strand the queue, because no further idle edge
	// arrives for a now-genuinely-idle session.
	s.drainHead(ctx, platformID, sessionID, true)
}

// drainHead sends the single oldest queued message. The caller MUST hold
// the per-session lock. No-op when the queue is empty, or (when
// trustIdle is false) when the session still reads as busy. On a send
// error the message stays at the head for a later retry.
//
// trustIdle distinguishes the two callers: Flush (session.idle edge)
// passes true because the edge itself proves the turn ended; the enqueue
// fast-path passes false because it has no such proof and must gate on
// the inferred status.
func (s *Service) drainHead(ctx context.Context, platformID, sessionID string, trustIdle bool) {
	// Busy gate: never send into a running turn — but only when we don't
	// already have an authoritative idle signal (see Flush).
	if !trustIdle {
		if running, ok := s.status.TurnRunning(ctx, platformID, sessionID); ok && running {
			return
		}
	}

	head, err := s.store.HeadQueuedMessage(platformID, sessionID)
	if err != nil {
		log.WithError(err).WithField("sessionID", sessionID).
			Warn("queuesvc: reading queue head")
		return
	}
	if head == nil {
		return // queue empty
	}

	req := platforms.SendMessageRequest{
		SessionID: head.SessionID,
		Message:   head.Text,
		Images:    decodeImages(head.ImagesJSON),
		Model:     head.Model,
		Agent:     head.Agent,
		Reasoning: head.Reasoning,
	}
	messageID := ""
	messageCreatedAt := int64(0)
	if completion, ok := s.status.(completionInferer); ok {
		var running, resolved bool
		messageID, messageCreatedAt, running, _, resolved = completion.LatestMessageState(ctx, head.Platform, sessionID)
		if !resolved && !trustIdle {
			return
		}
		if running && !trustIdle {
			return
		}
	}
	if err := s.sender.SendNow(ctx, head.Platform, req); err != nil {
		// Leave the message at the head; a later idle edge retries.
		log.WithError(err).WithField("sessionID", sessionID).
			Warn("queuesvc: sending queued message")
		return
	}
	if _, err := s.store.DeleteQueuedMessage(head.ID); err != nil {
		log.WithError(err).WithField("messageID", head.ID).
			Warn("queuesvc: dequeuing sent message")
		return
	}
	// This send started a turn; block the enqueue fast-path until a real
	// session.idle edge confirms it finished.
	s.markDrained(sessionID, messageID, messageCreatedAt)
	s.fireNotify(sessionID)
}

// Sweep drains one message from every session whose queue is non-empty
// and whose turn is currently idle. It is the self-healing safety net for
// backlogs that never received a session.idle edge — rows stranded before
// a fix, or an edge swallowed by a lagging status poll. Unlike Flush it
// does NOT trust an authoritative idle edge (there is none), so it gates
// on the inferred status (trustIdle=false): a session that reads busy is
// left for the next sweep or its real idle edge.
//
// One message per session per sweep, matching the one-follow-up-per-turn
// contract: draining the head starts a turn, and the next sweep (or idle
// edge) drains the next.
func (s *Service) Sweep(ctx context.Context) {
	sessions, err := s.store.SessionsWithQueuedMessages()
	if err != nil {
		log.WithError(err).Warn("queuesvc: sweep listing sessions")
		return
	}
	for _, q := range sessions {
		// Honor the same status-blip guard as the enqueue fast-path: once a
		// message has drained, don't chain another into the same turn just
		// because the status poll blips to idle. A newer completed assistant
		// message proves the prior turn ended even if its idle edge was missed.
		guard, guarded := s.currentDrainGuard(q.SessionID)
		if guarded {
			completion, supported := s.status.(completionInferer)
			if !supported {
				continue
			}
			messageID, createdAt, _, completed, ok := completion.LatestMessageState(ctx, q.Platform, q.SessionID)
			if !ok || !completed || messageID == "" || createdAt <= guard.createdAt || messageID == guard.messageID {
				continue
			}
		}
		lock := s.lockFor(q.SessionID)
		lock.Lock()
		current, stillGuarded := s.currentDrainGuard(q.SessionID)
		if guarded != stillGuarded || (guarded && current.generation != guard.generation) {
			lock.Unlock()
			continue
		}
		if stillGuarded {
			s.clearDrainedSinceIdle(q.SessionID)
		}
		s.drainHead(ctx, q.Platform, q.SessionID, false)
		lock.Unlock()
	}
}

// List returns a session's pending follow-up queue, oldest first.
func (s *Service) List(platformID, sessionID string) ([]state.QueuedMessage, error) {
	return s.store.ListQueuedMessages(platformID, sessionID)
}

// Remove deletes a queued message by id, but only if it belongs to the
// given session (so one session can't mutate another's queue). Returns
// whether a row was removed.
func (s *Service) Remove(sessionID, id string) (bool, error) {
	_, owner, ok, err := s.store.GetQueuedMessageSession(id)
	if err != nil {
		return false, err
	}
	if !ok || owner != sessionID {
		return false, nil
	}
	removed, err := s.store.DeleteQueuedMessage(id)
	if err != nil {
		return false, err
	}
	if removed {
		s.fireNotify(sessionID)
	}
	return removed, nil
}

// Move reorders a queued message within its session by swapping it with
// the adjacent message in the given direction (-1 up, +1 down), only if
// the message belongs to the given session. Returns whether a swap
// happened (false at a boundary or on a mismatch).
func (s *Service) Move(sessionID, id string, direction int) (bool, error) {
	_, owner, ok, err := s.store.GetQueuedMessageSession(id)
	if err != nil {
		return false, err
	}
	if !ok || owner != sessionID {
		return false, nil
	}
	moved, err := s.store.MoveQueuedMessage(id, direction)
	if err != nil {
		return false, err
	}
	if moved {
		s.fireNotify(sessionID)
	}
	return moved, nil
}

func (s *Service) fireNotify(sessionID string) {
	if s.notify != nil {
		s.notify(sessionID)
	}
}

func encodeImages(imgs []platforms.ImageAttachment) string {
	if len(imgs) == 0 {
		return ""
	}
	b, err := json.Marshal(imgs)
	if err != nil {
		return ""
	}
	return string(b)
}

func decodeImages(s string) []platforms.ImageAttachment {
	if s == "" {
		return nil
	}
	var imgs []platforms.ImageAttachment
	if err := json.Unmarshal([]byte(s), &imgs); err != nil {
		return nil
	}
	return imgs
}
