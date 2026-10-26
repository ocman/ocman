// Package convstream produces Ocman conversation event streams: one Hub
// per owner keeps, for every subscribed session, a single upstream, a
// bounded replay log and the live overlay, and fans frames out to
// subscribers. See spec/conversation-events/design.md §6.
package convstream

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/NoUseFreak/ocman/internal/convevents"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Key names a session stream: owner-qualified platform id plus session id.
type Key struct{ Platform, SessionID string }

// Hub defaults.
const (
	defaultLinger      = 30 * time.Second
	subscriberBuffer   = 256
	maxTotalBytes      = 64 << 20
	settleTimeout      = 10 * time.Second
	queueReadTimeout   = 5 * time.Second
	initialBackoff     = 500 * time.Millisecond
	maxBackoff         = 30 * time.Second
	descendantNegative = 10 * time.Second
)

// ErrClosed is returned by Subscribe once the hub is shutting down.
var ErrClosed = errors.New("convstream: hub closed")

type Options struct {
	// Workers runs sources, settle reads and queue reads; they are joined
	// with the owning module.
	Workers *module.Workers
	// Descendant reports whether child descends from root, for routing
	// descendants' prompts and child status. Optional.
	Descendant func(ctx context.Context, root Key, child string) bool
	// Queue reads a session's held messages after a queue change. Optional.
	Queue func(ctx context.Context, key Key) ([]convevents.QueuedMessage, error)
	// Linger keeps a source attached after its last subscriber leaves.
	Linger time.Duration
	// Backoff is the first source reconnect delay; it doubles to maxBackoff.
	Backoff time.Duration
	Now     func() time.Time
}

type Hub struct {
	opts Options

	mu       sync.Mutex
	closed   bool
	sessions map[Key]*session
	// ledgers outlive their sessions so a revisit composes unconfirmed content.
	ledgers map[Key]*ledger

	// totalBytes counts retained log bytes across sessions.
	totalBytes atomic.Int64
	maxTotal   int64
}

func NewHub(opts Options) *Hub {
	if opts.Workers == nil {
		opts.Workers = &module.Workers{}
	}
	if opts.Linger == 0 {
		opts.Linger = defaultLinger
	}
	if opts.Backoff == 0 {
		opts.Backoff = initialBackoff
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Hub{opts: opts, sessions: map[Key]*session{}, ledgers: map[Key]*ledger{}, maxTotal: maxTotalBytes}
}

// Subscribe attaches to key's stream, starting its source with open when
// none is attached. It returns once the source has connected (or once a
// source that was connected is reconnecting), with the hello and replay
// for after ("<epoch>:<seq>", or "" for a cold connect). A source that
// fails before it ever connected returns its error.
func (h *Hub) Subscribe(ctx context.Context, key Key, open func() Source, after string) (*Subscription, error) {
	s, err := h.acquire(key, open)
	if err != nil {
		return nil, err
	}
	// A subscriber waiting for the first connection is attached by
	// connectLocked itself, before the source can emit anything.
	w := &waiter{after: after, sub: make(chan *Subscription, 1)}
	for {
		s.mu.Lock()
		select {
		case sub := <-w.sub:
			s.mu.Unlock()
			return sub, nil
		default:
		}
		if s.everConnected {
			sub := s.attachLocked(after)
			s.mu.Unlock()
			return sub, nil
		}
		if !s.running {
			err := s.err
			s.dropWaiterLocked(w)
			s.mu.Unlock()
			h.release(s)
			return nil, err
		}
		if !w.queued {
			w.queued = true
			s.waiters = append(s.waiters, w)
		}
		changed := s.changed
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.dropWaiterLocked(w)
			s.mu.Unlock()
			h.release(s)
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

type waiter struct {
	after  string
	queued bool
	sub    chan *Subscription
}

// dropWaiterLocked forgets w and closes a subscription attached to it that
// was never handed out.
func (s *session) dropWaiterLocked(w *waiter) {
	s.waiters = slices.DeleteFunc(s.waiters, func(o *waiter) bool { return o == w })
	select {
	case sub := <-w.sub:
		s.killLocked(sub, "")
	default:
	}
}

func (h *Hub) acquire(key Key, open func() Source) (*session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, ErrClosed
	}
	s := h.sessions[key]
	if s == nil {
		s = newSession(h, key)
		s.ledger = h.ledgerLocked(key)
		h.sessions[key] = s
	}
	s.interest++
	if s.linger != nil {
		s.linger.Stop()
		s.linger = nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		if err := s.startLocked(open()); err != nil {
			s.interest--
			if s.interest == 0 {
				delete(h.sessions, key)
			}
			return nil, err
		}
	}
	return s, nil
}

// release drops one subscriber's interest; the last one starts the linger.
func (h *Hub) release(s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s.interest--
	if s.interest > 0 {
		return
	}
	s.mu.Lock()
	running := s.running
	s.mu.Unlock()
	if !running || h.closed {
		h.detachLocked(s)
		return
	}
	s.linger = time.AfterFunc(h.opts.Linger, func() { h.expire(s) })
}

func (h *Hub) expire(s *session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s.interest == 0 && h.sessions[s.key] == s {
		h.detachLocked(s)
	}
}

func (h *Hub) detachLocked(s *session) {
	if h.sessions[s.key] == s {
		delete(h.sessions, s.key)
	}
	if s.linger != nil {
		s.linger.Stop()
		s.linger = nil
	}
	s.mu.Lock()
	h.totalBytes.Add(-int64(s.log.bytes))
	s.log = newLog()
	s.detached = true
	// No stream will report this turn's end; its content waits for history.
	s.ledger.settle()
	s.ledger.touch(h.opts.Now())
	cancel := s.cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// ledgerLocked returns key's ledger, creating it, and forgets expired ones.
func (h *Hub) ledgerLocked(key Key) *ledger {
	h.sweepLocked()
	l := h.ledgers[key]
	if l == nil {
		l = &ledger{touched: h.opts.Now()}
		h.ledgers[key] = l
	}
	return l
}

func (h *Hub) sweepLocked() {
	now := h.opts.Now()
	for key, l := range h.ledgers {
		if h.sessions[key] == nil && l.expired(now) {
			delete(h.ledgers, key)
		}
	}
}

// Composed describes what Hub.Compose added to a stored read.
type Composed struct {
	// Cursor is the stream position the read covers, or "" when no stream is
	// attached; a client replays only events after it.
	Cursor string
	// MessageIDs are added messages history has not stored; history paging
	// offsets must not count them.
	MessageIDs []string
}

// Compose lays the stream's unconfirmed content over a stored conversation
// read of key and releases what the read already stores.
func (h *Hub) Compose(key Key, d *platforms.SessionDetail, limit, offset int) Composed {
	h.mu.Lock()
	h.sweepLocked()
	s, l := h.sessions[key], h.ledgers[key]
	h.mu.Unlock()
	if s == nil {
		if l == nil {
			return Composed{}
		}
		return Composed{MessageIDs: l.compose(d, limit, offset, h.opts.Now())}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Composed{MessageIDs: s.ledger.compose(d, limit, offset, h.opts.Now())}
	if s.everConnected && !s.detached {
		out.Cursor = convevents.FormatCursor(s.log.epoch, s.log.last)
	}
	return out
}

// Publish adds a synthetic event to an attached session's stream; it is
// dropped when no source is attached for key.
func (h *Hub) Publish(key Key, ev convevents.Event) {
	h.mu.Lock()
	s := h.sessions[key]
	h.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.everConnected {
		s.handleLocked(ev, false)
	}
}

func (h *Hub) descendant(key Key, child string) bool {
	h.mu.Lock()
	s := h.sessions[key]
	h.mu.Unlock()
	return s != nil && s.isDescendant(child)
}

// Close detaches every source and ends every subscription. The caller joins
// Options.Workers afterwards.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	sessions := make([]*session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	for _, s := range sessions {
		h.detachLocked(s)
	}
	h.mu.Unlock()
	for _, s := range sessions {
		s.mu.Lock()
		for sub := range s.subs {
			s.killLocked(sub, "")
		}
		s.mu.Unlock()
	}
}

// account adds a log size change to the global total and, past the global
// cap, trims the growing log to half its size. Called with s.mu held.
func (h *Hub) account(s *session, delta int) {
	if h.totalBytes.Add(int64(delta)) <= h.maxTotal {
		return
	}
	before := s.log.bytes
	s.log.trim(s.log.bytes/2, s.log.maxEntries)
	h.totalBytes.Add(int64(s.log.bytes - before))
}

// frameMsg is one pre-encoded SSE frame and the seq it carries.
type frameMsg struct {
	seq   uint64
	frame []byte
}

// Subscription is one consumer of a session stream. Hello and Replay are
// written first, then frames from C until Done closes.
type Subscription struct {
	Epoch  string
	Hello  frameMsg
	Replay []frameMsg

	ch     chan frameMsg
	done   chan struct{}
	reason string
	s      *session
	once   sync.Once
}

// C delivers live frames in order.
func (sub *Subscription) C() <-chan frameMsg { return sub.ch }

// Done closes when the hub ends the subscription; Reason then says why
// (a stream.reset reason, or "" on shutdown).
func (sub *Subscription) Done() <-chan struct{} { return sub.done }

func (sub *Subscription) Reason() string {
	sub.s.mu.Lock()
	defer sub.s.mu.Unlock()
	return sub.reason
}

// Close ends the subscription; it is idempotent.
func (sub *Subscription) Close() {
	sub.once.Do(func() {
		sub.s.mu.Lock()
		delete(sub.s.subs, sub)
		sub.s.mu.Unlock()
		sub.s.hub.release(sub.s)
	})
}
