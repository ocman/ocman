package convstream

import (
	"context"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/convevents"
	"github.com/ocman/ocman/internal/platforms"
)

// Source is one session's live upstream. Watch streams until ctx ends or
// the upstream closes. It calls sink.Connected once, after any bootstrap
// and before its first Emit; an error before Connected means the session
// has no live instance. Every Watch call is a new connection without
// continuity, so the hub starts a new epoch for it.
type Source interface {
	Watch(ctx context.Context, sink Sink) error
}

// Settler is implemented by sources that can read a session's settled
// lifecycle, for turning an idle edge into a terminal status.
type Settler interface {
	Settle(ctx context.Context) (*platforms.SessionLifecycle, error)
}

type Sink interface {
	Connected(Seed)
	// Emit accepts mapped events. Session status is the raw observation:
	// the hub settles terminal status, derives waiting and turn edges.
	Emit(convevents.Event, Meta)
	// QueueChanged asks the hub to read the session queue.
	QueueChanged()
}

// Seed is the source's view of live state when it connects.
type Seed struct {
	// Status nil means unknown; the hub settles it.
	Status  *convevents.SessionStatusEvent
	Prompts []convevents.Prompt
	// Background nil keeps the last known.
	Background *convevents.BackgroundChanged
}

type Meta struct {
	// Replayed marks frames the owner already acted on.
	Replayed bool
}

var errSourceEnded = errors.New("convstream: source ended before connecting")

func (s *session) startLocked(src Source) error {
	ctx, cancel := context.WithCancel(context.Background())
	started := s.hub.opts.Workers.Go(func(wctx context.Context) {
		stop := context.AfterFunc(wctx, cancel)
		defer stop()
		s.run(ctx, src)
	})
	if !started {
		cancel()
		return ErrClosed
	}
	s.source, s.cancel, s.running, s.err = src, cancel, true, nil
	return nil
}

// run watches src until the session detaches, reconnecting with backoff
// once it has connected; a source that never connects fails the session.
func (s *session) run(ctx context.Context, src Source) {
	backoff := s.hub.opts.Backoff
	for {
		sink := s.newSink()
		err := src.Watch(ctx, sink)
		s.mu.Lock()
		sink.closed = true
		connected := sink.connected
		if ctx.Err() != nil || !s.everConnected {
			s.running = false
			switch {
			case ctx.Err() != nil:
				s.err = ErrClosed
			case err == nil:
				s.err = errSourceEnded
			default:
				s.err = err
			}
			s.broadcastLocked()
			s.mu.Unlock()
			return
		}
		s.connected, s.ov.liveConnect = false, false
		if err != nil && !errors.Is(err, platforms.ErrSSEIdleTimeout) {
			s.controlLocked(s.sourceError(err))
		}
		s.mu.Unlock()
		if connected {
			backoff = s.hub.opts.Backoff
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (s *session) sourceError(err error) convevents.StreamError {
	code := convevents.CodePlatformUnreachable
	if remote, _ := platforms.SplitPlatformID(s.key.Platform); remote != "" {
		code = convevents.CodeOwnerOffline
	}
	return convevents.StreamError{Code: code, Message: err.Error(), Retryable: true}
}

// sessionSink is one connection's sink; it goes stale when the connection
// ends, so late callbacks never reach a newer epoch.
type sessionSink struct {
	s *session
	// guarded by s.mu
	gen               uint64
	connected, closed bool
}

func (s *session) newSink() *sessionSink {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	return &sessionSink{s: s, gen: s.gen}
}

func (k *sessionSink) staleLocked() bool { return k.closed || k.gen != k.s.gen || k.s.detached }

func (k *sessionSink) Connected(seed Seed) {
	prompts := make([]convevents.Prompt, 0, len(seed.Prompts))
	for _, p := range seed.Prompts {
		if k.s.routes(p.SessionID) {
			prompts = append(prompts, p)
		}
	}
	seed.Prompts = prompts
	k.s.mu.Lock()
	defer k.s.mu.Unlock()
	if k.staleLocked() || k.connected {
		return
	}
	k.connected = true
	k.s.connectLocked(seed)
}

func (k *sessionSink) Emit(ev convevents.Event, meta Meta) {
	if pa, ok := ev.(convevents.PromptAsked); ok && !k.s.routes(pa.Prompt.SessionID) {
		return
	}
	k.s.mu.Lock()
	defer k.s.mu.Unlock()
	if k.staleLocked() {
		return
	}
	k.s.handleLocked(ev, meta.Replayed)
}

func (k *sessionSink) QueueChanged() {
	k.s.mu.Lock()
	defer k.s.mu.Unlock()
	if !k.staleLocked() {
		k.s.readQueueLocked()
	}
}
