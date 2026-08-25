// Package queuesvc owns the follow-up message queue: prompts the user
// explicitly defers (Ctrl/Cmd+Enter in the composer) are appended here
// and drained one at a time on each session.idle edge. The queue lives
// server-side (in state.db) so it is shared across every connected
// client and survives a client moving machines — #58.
//
// A plain Enter send never reaches this package: it goes straight to the
// platform, mid-turn included, so the running turn picks it up instead of
// the user waiting out the whole turn.
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
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// Store is the consumer-side subset of *state.DB the queue needs.
type Store interface {
	EnqueueMessage(ctx context.Context, m state.QueuedMessage) error
	CountQueuedMessages(ctx context.Context, platform, sessionID string) (int, error)
	HeadQueuedMessage(ctx context.Context, platform, sessionID string) (*state.QueuedMessage, error)
	ListQueuedMessages(ctx context.Context, platform, sessionID string) ([]state.QueuedMessage, error)
	ListQueuedMessagesAnyPlatform(ctx context.Context, sessionID string) ([]state.QueuedMessage, error)
	DeleteQueuedMessage(ctx context.Context, id string) (bool, error)
	MoveQueuedMessage(ctx context.Context, id string, direction int) (bool, error)
	GetQueuedMessageSession(ctx context.Context, id string) (platform, sessionID string, ok bool, err error)
	SessionsWithQueuedMessages(ctx context.Context) ([]state.QueuedSession, error)
	RecordQueuedMessageFailure(ctx context.Context, id, reason string) (bool, error)
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

// sessionKey is a session's full identity: the same bare session id can
// exist on several machines (a local `opencode/s1` and a remote
// `r-A:opencode/s1`), so every map in this package is keyed by the pair.
// Keying by the bare id let one machine's idle edge drain — or suppress —
// another machine's queue.
type sessionKey struct {
	Platform  string
	SessionID string
}

// Service manages the follow-up queue.
type Service struct {
	store  Store
	sender Sender
	status StatusInferer
	notify func(context.Context, string, string) // optional; broadcast queue.updated

	// One lock per session serializes flush drains so an enqueue-driven
	// flush and an idle-driven flush cannot pop the same head twice.
	mu    sync.Mutex
	locks map[sessionKey]*sync.Mutex

	// drainGuards[key] records the source message visible before a queued send
	// for that session and we have NOT yet seen a real session.idle edge.
	// It gates the enqueue fast-path so a burst of enqueues can't chain
	// multiple sends into one turn just because the status poll blips to
	// idle (the last message being a user message reads as idle). A genuine
	// session.idle or a newer completed assistant message clears it. Guarded by mu.
	drainGuards    map[sessionKey]drainGuard
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
func New(store Store, sender Sender, status StatusInferer, notify func(context.Context, string, string)) *Service {
	return &Service{
		store:       store,
		sender:      sender,
		status:      status,
		notify:      notify,
		locks:       map[sessionKey]*sync.Mutex{},
		drainGuards: map[sessionKey]drainGuard{},
	}
}

func (s *Service) markDrained(key sessionKey, messageID string, createdAt int64) {
	s.mu.Lock()
	s.nextGeneration++
	s.drainGuards[key] = drainGuard{generation: s.nextGeneration, messageID: messageID, createdAt: createdAt}
	s.mu.Unlock()
}

func (s *Service) currentDrainGuard(key sessionKey) (drainGuard, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	guard, ok := s.drainGuards[key]
	return guard, ok
}

func (s *Service) clearDrainedSinceIdle(key sessionKey) {
	s.mu.Lock()
	delete(s.drainGuards, key)
	s.mu.Unlock()
}

func (s *Service) lockFor(key sessionKey) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := s.locks[key]
	if !ok {
		m = &sync.Mutex{}
		s.locks[key] = m
	}
	return m
}

// ErrEmptyMessage is returned by Enqueue when a message has neither text
// nor images. Transports map it to a 400 (same shape sessionsvc uses).
var ErrEmptyMessage = errors.New("message or images required")

// Enqueue appends a message to the session's queue.
//
// forceQueue is the caller's authoritative "hold this" signal — the
// user's Ctrl/Cmd+Enter gesture, or an internal deferral (child results,
// scheduled prompts). When true, the message is HELD: it is never drained
// here and waits for the next session.idle edge. It is deliberately NOT
// derived from inferred status, which reads the lagging DB and can wrongly
// report idle mid-turn (#58).
//
// When forceQueue is false the fast path may drain it now:
// only if it is the sole queued message AND nothing has drained since the
// last idle edge. That "first item only" rule keeps a busy session's
// queue intact even if the status poll blips to idle. A genuine
// session.idle (via Flush) drains a backlog.
func (s *Service) Enqueue(ctx context.Context, platformID string, forceQueue bool, req platforms.SendMessageRequest) error {
	if req.Message == "" && len(req.Images) == 0 {
		return ErrEmptyMessage
	}
	m := state.QueuedMessage{
		ID:         newID(),
		Platform:   platformID,
		SessionID:  req.SessionID,
		Text:       req.Message,
		ImagesJSON: encodeImages(req.Images),
		Model:      req.Model,
		Agent:      req.Agent,
		Reasoning:  req.Reasoning,
		CreatedAt:  nowMillis(),
	}

	key := sessionKey{Platform: platformID, SessionID: req.SessionID}
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()

	existing, err := s.store.CountQueuedMessages(ctx, platformID, req.SessionID)
	if err != nil {
		return err
	}
	if err := s.store.EnqueueMessage(ctx, m); err != nil {
		return err
	}
	// The caller asked to hold the message. Do NOT mark
	// drained here — nothing has been sent yet. Marking drained would
	// disarm the Sweep backstop, so a forceQueue message whose
	// session.idle edge never arrives (watcher disconnected, edge not
	// emitted) would be stranded forever with no send and no idle edge to
	// clear the guard. The busy gate in drainHead (trustIdle=false) is
	// what keeps Sweep from sending into a still-running turn; the guard
	// is only set once a message is actually sent (in drainHead).
	if forceQueue {
		s.fireNotify(ctx, key)
		return nil
	}
	s.fireNotify(ctx, key)

	// Idle send fast path — see doc comment.
	if _, guarded := s.currentDrainGuard(key); existing == 0 && !guarded {
		s.drainHead(ctx, key, false)
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
// platformID is required: a session's identity is (platform, sessionID),
// and the same bare id can exist on several machines. Flushing on the bare
// id let a local instance's idle edge drain a remote session's queue.
func (s *Service) Flush(ctx context.Context, platformID, sessionID string) {
	key := sessionKey{Platform: platformID, SessionID: sessionID}
	lock := s.lockFor(key)
	lock.Lock()
	defer lock.Unlock()
	// A real idle edge re-arms the enqueue fast-path: the previous turn
	// has genuinely finished, so the next drained message starts a fresh
	// turn.
	s.clearDrainedSinceIdle(key)
	// trustIdle=true: session.idle IS the authoritative turn-finished
	// signal, so the flush acts on the edge alone.
	//
	// Since #488 the status gate this skips is no longer a guess derived
	// from the last message's shape — it reads the agent's own turn state
	// — so the old "never derive from inferred status" caveat is gone.
	// Trusting the edge outright still matters for a narrower reason: it
	// makes the drain independent of the order in which session.idle and
	// session.status arrive, and of a failed status snapshot. A gate that
	// read busy at this instant for either reason would swallow the drain
	// and strand the queue, since no second idle edge is coming.
	s.drainHead(ctx, key, true)
}

// drainHead sends the single oldest queued message. The caller MUST hold
// the per-session lock. No-op when the queue is empty, or (when
// trustIdle is false) when the session still reads as busy. On a send
// error the message stays at the head for a later retry.
//
// trustIdle distinguishes the two callers: Flush (session.idle edge)
// passes true because the edge itself proves the turn ended; the enqueue
// fast-path passes false because it has no such proof and must gate on
// the session's reported status.
func (s *Service) drainHead(ctx context.Context, key sessionKey, trustIdle bool) {
	sessionID := key.SessionID
	// Busy gate: never send into a running turn — but only when we don't
	// already have an authoritative idle signal (see Flush).
	if !trustIdle {
		if running, ok := s.status.TurnRunning(ctx, key.Platform, sessionID); ok && running {
			return
		}
	}

	head, err := s.store.HeadQueuedMessage(ctx, key.Platform, sessionID)
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
		// Leave the message at the head; a later idle edge retries. But
		// count the failure: the drain is strictly head-first, so a
		// message that can never send (deleted session, unregistered
		// platform) would otherwise block every later message on this
		// session forever, with only a log line to show for it.
		blocked, recErr := s.store.RecordQueuedMessageFailure(ctx, head.ID, err.Error())
		if recErr != nil {
			log.WithError(recErr).WithField("messageID", head.ID).
				Warn("queuesvc: recording send failure")
		}
		entry := log.WithError(err).WithField("sessionID", sessionID)
		if blocked {
			entry.WithField("messageID", head.ID).
				Error("queuesvc: queued message set aside after repeated send failures")
			s.fireNotify(ctx, key)
		} else {
			entry.Warn("queuesvc: sending queued message")
		}
		return
	}
	if _, err := s.store.DeleteQueuedMessage(ctx, head.ID); err != nil {
		log.WithError(err).WithField("messageID", head.ID).
			Warn("queuesvc: dequeuing sent message")
		return
	}
	// This send started a turn; block the enqueue fast-path until a real
	// session.idle edge confirms it finished.
	s.markDrained(key, messageID, messageCreatedAt)
	s.fireNotify(ctx, key)
}

// Sweep drains one message from every session whose queue is non-empty
// and whose turn is currently idle. It is the self-healing safety net for
// backlogs that never received a session.idle edge — rows stranded before
// a fix, or an edge lost because ocman wasn't connected to the instance
// when it fired. Unlike Flush it does NOT trust an authoritative idle edge
// (there is none), so it gates on the session's reported status
// (trustIdle=false): a session that reads busy is left for the next sweep
// or its real idle edge.
//
// One message per session per sweep, matching the one-follow-up-per-turn
// contract: draining the head starts a turn, and the next sweep (or idle
// edge) drains the next.
func (s *Service) Sweep(ctx context.Context) {
	sessions, err := s.store.SessionsWithQueuedMessages(ctx)
	if err != nil {
		log.WithError(err).Warn("queuesvc: sweep listing sessions")
		return
	}
	for _, q := range sessions {
		// Honor the same status-blip guard as the enqueue fast-path: once a
		// message has drained, don't chain another into the same turn just
		// because the status poll blips to idle. A newer completed assistant
		// message proves the prior turn ended even if its idle edge was missed.
		key := sessionKey{Platform: q.Platform, SessionID: q.SessionID}
		guard, guarded := s.currentDrainGuard(key)
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
		lock := s.lockFor(key)
		lock.Lock()
		current, stillGuarded := s.currentDrainGuard(key)
		if guarded != stillGuarded || (guarded && current.generation != guard.generation) {
			lock.Unlock()
			continue
		}
		if stillGuarded {
			s.clearDrainedSinceIdle(key)
		}
		s.drainHead(ctx, key, false)
		lock.Unlock()
	}
}

// List returns a session's pending follow-up queue, oldest first.
func (s *Service) List(ctx context.Context, platformID, sessionID string) ([]state.QueuedMessage, error) {
	return s.store.ListQueuedMessages(ctx, platformID, sessionID)
}

// ListAnyPlatform is the one deliberate cross-platform read: the queue
// list endpoint's `platform` query parameter is optional, so an older
// client (or a deep link) can ask for a session's queue without naming its
// owner. It is read-only and never feeds a drain — every mutating path
// resolves the full (platform, sessionID) identity first.
func (s *Service) ListAnyPlatform(ctx context.Context, sessionID string) ([]state.QueuedMessage, error) {
	return s.store.ListQueuedMessagesAnyPlatform(ctx, sessionID)
}

// Remove deletes a queued message by id, but only if it belongs to the
// given session (so one session can't mutate another's queue). Returns
// whether a row was removed.
func (s *Service) Remove(ctx context.Context, sessionID, id string) (bool, error) {
	platform, owner, ok, err := s.store.GetQueuedMessageSession(ctx, id)
	if err != nil {
		return false, err
	}
	if !ok || owner != sessionID {
		return false, nil
	}
	removed, err := s.store.DeleteQueuedMessage(ctx, id)
	if err != nil {
		return false, err
	}
	if removed {
		// The row's own platform is authoritative: the caller only knows
		// the bare session id, which several machines can share.
		s.fireNotify(ctx, sessionKey{Platform: platform, SessionID: sessionID})
	}
	return removed, nil
}

// Move reorders a queued message within its session by swapping it with
// the adjacent message in the given direction (-1 up, +1 down), only if
// the message belongs to the given session. Returns whether a swap
// happened (false at a boundary or on a mismatch).
func (s *Service) Move(ctx context.Context, sessionID, id string, direction int) (bool, error) {
	platform, owner, ok, err := s.store.GetQueuedMessageSession(ctx, id)
	if err != nil {
		return false, err
	}
	if !ok || owner != sessionID {
		return false, nil
	}
	moved, err := s.store.MoveQueuedMessage(ctx, id, direction)
	if err != nil {
		return false, err
	}
	if moved {
		s.fireNotify(ctx, sessionKey{Platform: platform, SessionID: sessionID})
	}
	return moved, nil
}

func (s *Service) fireNotify(ctx context.Context, key sessionKey) {
	if s.notify != nil {
		s.notify(ctx, key.Platform, key.SessionID)
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
