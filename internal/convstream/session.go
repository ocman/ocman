package convstream

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/convevents"
)

type session struct {
	hub *Hub
	key Key
	// interest and linger are guarded by hub.mu.
	interest int
	linger   *time.Timer

	mu            sync.Mutex
	source        Source
	cancel        context.CancelFunc
	running       bool
	everConnected bool
	connected     bool
	detached      bool
	err           error
	changed       chan struct{}
	gen           uint64
	subs          map[*Subscription]struct{}
	waiters       []*waiter // subscribers waiting for the first connection
	log           *sessionLog
	ov            overlay
	// ledger is shared with later sessions for the same key (Hub.ledgers).
	ledger   *ledger
	st       statusState
	queueGen uint64

	descMu sync.Mutex
	desc   map[string]descendant
}

type descendant struct {
	yes   bool
	until time.Time
}

func newSession(h *Hub, key Key) *session {
	return &session{hub: h, key: key, changed: make(chan struct{}), subs: map[*Subscription]struct{}{},
		log: newLog(), ov: newOverlay(), ledger: &ledger{}, st: newStatusState(), desc: map[string]descendant{}}
}

func (s *session) broadcastLocked() {
	close(s.changed)
	s.changed = make(chan struct{})
}

func (s *session) now() int64 { return s.hub.opts.Now().UnixMilli() }

// connectLocked starts serving a connection. A reconnect cannot continue
// the previous one, so it opens a new epoch and resets its subscribers.
func (s *session) connectLocked(seed Seed) {
	// Settle reads from the old connection cannot publish into this epoch.
	gen := s.st.gen + 1
	s.st = newStatusState()
	s.st.gen = gen
	if s.everConnected {
		s.hub.totalBytes.Add(-int64(s.log.bytes))
		s.log = newLog()
		live := s.ov
		s.ov = newOverlay()
		s.ov.background, s.ov.queue = live.background, live.queue
		for sub := range s.subs {
			s.killLocked(sub, convevents.ResetSourceRestarted)
		}
	}
	s.everConnected, s.connected, s.ov.liveConnect = true, true, true
	for _, p := range seed.Prompts {
		s.ov.ask(p)
	}
	if bg := seed.Background; bg != nil {
		if bg.Tasks == nil {
			bg.Tasks = []convevents.BackgroundTask{}
		}
		s.ov.background = *bg
	}
	if seed.Status != nil {
		s.st.seed(*seed.Status)
		if !s.st.turn {
			// A turn that ended while the source was away never sends its idle edge.
			s.ledger.settle()
		}
	} else {
		s.st.gen++
		s.scheduleSettleLocked(s.st.gen, false)
	}
	s.st.emitted = s.effective()
	for _, w := range s.waiters {
		w.sub <- s.attachLocked(w.after)
	}
	s.waiters = nil
	s.broadcastLocked()
}

func (s *session) attachLocked(after string) *Subscription {
	sub := &Subscription{Epoch: s.log.epoch, ch: make(chan frameMsg, subscriberBuffer), done: make(chan struct{}), s: s}
	hello := convevents.StreamHello{Protocol: convevents.Version}
	seq := s.log.last
	if epoch, at, ok := convevents.ParseCursor(after); ok && s.log.resumable(epoch, at) {
		hello.Resumed, seq = true, at
		sub.Replay = s.log.after(at)
	} else {
		ov := s.ov.live(s.effective(), s.ledger.snapshot())
		hello.Overlay = &ov
	}
	sub.Hello = frameMsg{seq: seq, frame: s.encode(hello, seq, false, true)}
	s.subs[sub] = struct{}{}
	return sub
}

func (s *session) killLocked(sub *Subscription, reason string) {
	select {
	case <-sub.done:
		return
	default:
	}
	sub.reason = reason
	close(sub.done)
	delete(s.subs, sub)
}

// publishLocked logs ev under the next seq and fans it out. A subscriber
// whose buffer is full is reset rather than blocking the producer.
func (s *session) publishLocked(ev convevents.Event, replayed bool) {
	seq := s.log.next()
	frame := s.encode(ev, seq, replayed, true)
	if frame == nil {
		return
	}
	supersedes, key := compaction(ev)
	s.hub.account(s, s.log.append(seq, frame, supersedes, key))
	s.fanoutLocked(frameMsg{seq: seq, frame: frame})
}

func (s *session) fanoutLocked(m frameMsg) {
	for sub := range s.subs {
		select {
		case sub.ch <- m:
		default:
			s.killLocked(sub, convevents.ResetSlowConsumer)
		}
	}
}

// controlLocked sends a connection-level frame that is not logged and
// carries no SSE id, so it never moves a client's cursor.
func (s *session) controlLocked(ev convevents.Event) {
	if frame := s.encode(ev, s.log.last, false, false); frame != nil {
		s.fanoutLocked(frameMsg{seq: s.log.last, frame: frame})
	}
}

// encode builds the SSE frame for ev; it is encoded once and shared.
func (s *session) encode(ev convevents.Event, seq uint64, replayed, withID bool) []byte {
	env := convevents.Envelope{V: convevents.Version, Epoch: s.log.epoch, Seq: seq, SessionID: s.key.SessionID,
		Platform: s.key.Platform, At: s.now(), Replayed: replayed, Event: ev}
	data, err := json.Marshal(env)
	if err != nil {
		log.WithError(err).WithField("type", ev.EventType()).Warn("convstream: dropping unencodable event")
		return nil
	}
	return sseFrame(env.Cursor(), withID, data)
}

func sseFrame(cursor string, withID bool, data []byte) []byte {
	b := make([]byte, 0, len(data)+len(cursor)+32)
	if withID {
		b = append(b, "id: "...)
		b = append(b, cursor...)
		b = append(b, '\n')
	}
	b = append(b, "event: oce\ndata: "...)
	b = append(b, data...)
	return append(b, "\n\n"...)
}

// controlFrame encodes a reset or error for this subscription's epoch at
// the last seq it delivered.
func (sub *Subscription) controlFrame(ev convevents.Event, seq uint64) []byte {
	sub.s.mu.Lock()
	defer sub.s.mu.Unlock()
	env := convevents.Envelope{V: convevents.Version, Epoch: sub.Epoch, Seq: seq, SessionID: sub.s.key.SessionID,
		Platform: sub.s.key.Platform, At: sub.s.now(), Event: ev}
	data, err := json.Marshal(env)
	if err != nil {
		return nil
	}
	return sseFrame("", false, data)
}

// compaction names what ev supersedes in the log and the key it is itself
// superseded under (design §6.4). Edges, removals and history are kept.
func compaction(ev convevents.Event) (supersedes []string, key string) {
	switch e := ev.(type) {
	case convevents.PartDelta:
		return nil, "part:" + e.PartID
	case convevents.PartUpserted:
		if e.Final {
			return []string{"part:" + e.Part.ID}, "part:" + e.Part.ID
		}
		return nil, "part:" + e.Part.ID
	case convevents.SessionStatusEvent:
		return []string{"status"}, "status"
	case convevents.BackgroundChanged:
		return []string{"background"}, "background"
	case convevents.QueueChanged:
		return []string{"queue"}, "queue"
	case convevents.HistoryCommitted:
		return []string{"history"}, "history"
	case convevents.PromptAsked:
		return nil, "prompt:" + e.Prompt.ID
	case convevents.PromptJudge:
		return nil, "prompt:" + e.PromptID
	case convevents.PromptResolved:
		return []string{"prompt:" + e.PromptID}, ""
	}
	return nil, ""
}

// routes reports whether a prompt from sessionID belongs on this stream.
func (s *session) routes(sessionID string) bool {
	return sessionID == "" || sessionID == s.key.SessionID || s.isDescendant(sessionID)
}

func (s *session) isDescendant(child string) bool {
	fn := s.hub.opts.Descendant
	if fn == nil || child == "" || child == s.key.SessionID {
		return false
	}
	now := s.hub.opts.Now()
	s.descMu.Lock()
	d, ok := s.desc[child]
	s.descMu.Unlock()
	if ok && (d.yes || now.Before(d.until)) {
		return d.yes
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	yes := fn(ctx, s.key, child)
	s.descMu.Lock()
	s.desc[child] = descendant{yes: yes, until: now.Add(descendantNegative)}
	s.descMu.Unlock()
	return yes
}

func (s *session) readQueueLocked() {
	read := s.hub.opts.Queue
	if read == nil {
		return
	}
	s.queueGen++
	gen := s.queueGen
	s.hub.opts.Workers.Go(func(ctx context.Context) {
		ctx, cancel := context.WithTimeout(ctx, queueReadTimeout)
		msgs, err := read(ctx, s.key)
		cancel()
		if err != nil {
			return
		}
		if msgs == nil {
			msgs = []convevents.QueuedMessage{}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if gen == s.queueGen {
			s.handleLocked(convevents.QueueChanged{Messages: msgs}, false)
		}
	})
}
