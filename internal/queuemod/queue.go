package queuemod

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/worker"
)

// queueSweepInterval is a recovery backstop for rows whose idle event was
// missed, including rows left by a crash. Idle events enqueue a flush now.
const queueSweepInterval = time.Minute

type queueFlush struct {
	ctx        context.Context
	platformID string
	sessionID  string
	cooled     string // provider that just cooled under the session; continue it
}

// runQueueSweep periodically drains one message from every idle session
// with a non-empty follow-up queue. It self-heals backlogs stranded by a
// missing/swallowed session.idle edge (e.g. rows queued before a fix).
// Runs one immediate sweep at startup, then on the interval.
func (s *Module) runQueueSweep(ctx context.Context) {
	if s.d.Store == nil {
		<-ctx.Done()
		return
	}
	tick := time.NewTicker(queueSweepInterval)
	defer tick.Stop()
	sweep := func() {
		if ctx.Err() != nil {
			return
		}
		// Like idle flushes, an admitted sweep must finish its send receipt.
		runWithRecover("queue-sweep", func() { s.queue.Sweep(context.WithoutCancel(ctx)) })
	}
	sweep()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			sweep()
		}
	}
}

func (s *Module) queueFlushWorker() *worker.Worker[queueFlush] {
	s.workerOnce.Do(func() {
		s.worker = worker.NewKeyed(func(item queueFlush) {
			runWithRecover("queue-flush", func() {
				if s.continueSession(item.ctx, item.platformID, item.sessionID, item.cooled) {
					return
				}
				s.queue.Flush(item.ctx, item.platformID, item.sessionID)
			})
		}, func(item queueFlush) string { return item.platformID + "\x00" + item.sessionID })
	})
	return s.worker
}

// lifecycle makes one owner-routed read of the session's settled status and
// latest message. It uses the bounded LifecycleReader so the cost does not
// grow with the transcript; Session(id, 1, 0) limits only the returned page.
func (i *Module) lifecycle(ctx context.Context, platform, sessionID string) (*platforms.SessionLifecycle, bool) {
	p, found := i.d.Adapter(ctx, platform, sessionID)
	if !found {
		return nil, false
	}
	if reader, ok := p.(platforms.LifecycleReader); ok {
		l, err := reader.SessionLifecycle(ctx, sessionID)
		if !errors.Is(err, platforms.ErrUnsupported) {
			return l, err == nil && l != nil
		}
	}
	// ponytail: full detail read for owners without the bounded read (an
	// older remote); drop once every remote serves SessionLifecycle.
	detail, err := p.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil {
		return nil, false
	}
	l := &platforms.SessionLifecycle{Status: detail.Session.Status}
	if len(detail.Messages) > 0 {
		message := detail.Messages[0]
		var data struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(message.Data, &data) != nil {
			return nil, false
		}
		l.LatestMessageID, l.LatestMessageCreated, l.LatestMessageRole = message.ID, message.TimeCreated, data.Role
	}
	return l, true
}

func (i *Module) TurnRunning(ctx context.Context, platform, sessionID string) (bool, bool) {
	l, ok := i.lifecycle(ctx, platform, sessionID)
	if !ok {
		return false, false
	}
	return l.Status == db.StatusBusy || l.Status == db.StatusLimited, true
}

func (i *Module) LatestMessageState(ctx context.Context, platform, sessionID string) (string, int64, bool, bool, bool) {
	l, ok := i.lifecycle(ctx, platform, sessionID)
	if !ok {
		return "", 0, false, false, false
	}
	running := l.Status == db.StatusBusy || l.Status == db.StatusLimited
	if l.LatestMessageID == "" {
		return "", 0, running, !running, true
	}
	return l.LatestMessageID, l.LatestMessageCreated, running, l.LatestMessageRole == "assistant" && !running, true
}

func (q *Module) SendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	return q.sendHeld(ctx, platformID, req)
}

// OnSessionIdle handles the session.idle edge. The flush is the
// authoritative send gate for held messages (#58) — a Ctrl+Enter enqueue
// never sends directly, so the idle edge is what delivers it. Runs in its
// serial worker so a slow platform send can't stall the SSE watcher and tests
// can wait for all queued flushes with Drain.
//
// platformID names the instance the edge came from. It is required: a bare
// session id is not an identity, and flushing on one let an idle edge from
// this machine drain a remote session that happened to share the id.
func (s *Module) OnSessionIdle(platformID, sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.ctx.Err() != nil || s.d.Store == nil || platformID == "" {
		return
	}
	// Synchronously, before the flush is enqueued: a held message must
	// see the dead provider's cooldown when it drains.
	cooled := s.recordQuotaCooldown(context.Background(), platformID, sessionID)
	if p, ok := s.fallAborted.LoadAndDelete(fallKey(platformID, sessionID)); ok && cooled == "" {
		cooled = p.(string)
	}
	// An admitted send must finish its durable receipt even during shutdown.
	// Run joins it before the database closes; cancellation stops admission.
	s.queueFlushWorker().Enqueue(queueFlush{ctx: context.WithoutCancel(s.ctx), platformID: platformID, sessionID: sessionID, cooled: cooled})
}

// broadcastQueueUpdated broadcasts that a session's follow-up queue
// changed (message enqueued or drained), carrying the session's full
// queue so clients apply it directly without a refetch. The messages key
// is always present (an empty queue sends []), so the client can trust it
// as authoritative rather than polling.
func (s *Module) BroadcastQueueUpdated(ctx context.Context, platform, sessionID string) {
	if sessionID == "" || platform == "" {
		return
	}
	// Scoped to the owning platform: a same-id session on another machine
	// has its own queue and its own broadcast. A read error just omits
	// messages so the client falls back to a refetch.
	var messages []queuedMessageView
	if msgs, err := s.queue.List(ctx, platform, sessionID); err == nil {
		messages = make([]queuedMessageView, 0, len(msgs))
		for _, m := range msgs {
			messages = append(messages, toQueuedMessageView(m))
		}
	}
	if messages != nil { // a failed read omits messages: never send a partial list
		messages = mergeQueued(messages, s.nativeQueuedViews(ctx, platform, sessionID))
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": sessionID,
		"messages":  messages,
	})
	if err != nil {
		return
	}
	s.d.Broadcast("ocman.queue.updated", payload)
}
