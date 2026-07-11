package server

import (
	"context"
	"encoding/json"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
)

// queueSweepInterval is how often the sweep drains standing backlogs that
// never received a session.idle edge. Short enough to feel responsive for
// stranded rows, long enough to be negligible load (one DISTINCT query +
// a status check per session with a backlog).
const queueSweepInterval = 15 * time.Second

// runQueueSweep periodically drains one message from every idle session
// with a non-empty follow-up queue. It self-heals backlogs stranded by a
// missing/swallowed session.idle edge (e.g. rows queued before a fix).
// Runs one immediate sweep at startup, then on the interval.
func (s *Server) runQueueSweep(ctx context.Context) {
	if s.stateDB == nil {
		return
	}
	tick := time.NewTicker(queueSweepInterval)
	defer tick.Stop()
	s.queueSvc().Sweep(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.queueSvc().Sweep(ctx)
		}
	}
}

// queueServiceFn builds the queue Service lazily, so tests can override
// it. nil means "construct the production service".
var queueServiceFn func(s *Server) *queuesvc.Service

// queueSvc returns the follow-up message queue service, building it on
// first use (mirrors loopSvc).
func (s *Server) queueSvc() *queuesvc.Service {
	s.queueSvcOnce.Do(func() {
		if queueServiceFn != nil {
			s.queueSvcCached = queueServiceFn(s)
			return
		}
		s.queueSvcCached = queuesvc.New(
			s.stateDB,
			&queueSender{s: s},
			&loopStatusInferer{s: s}, // reuse the loop engine's busy check
			func(sessionID string) { s.broadcastQueueUpdated(sessionID) },
		)
	})
	return s.queueSvcCached
}

// queueSender implements queuesvc.Sender by forwarding to sessionsvc's
// direct-send path. Only the queue drains through here; the composer
// handler enqueues, so there is no recursion.
type queueSender struct{ s *Server }

func (q *queueSender) SendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	return q.s.sessions.SendMessage(ctx, platformID, req)
}

// onSessionIdle handles the session.idle edge: it broadcasts idle (as
// before) and drains the session's follow-up queue. The flush is the
// authoritative send gate (#58) — enqueue never sends directly, so the
// idle edge is what actually delivers queued follow-ups. Runs in its own
// goroutine so a slow platform send can't stall the SSE watcher.
func (s *Server) onSessionIdle(sessionID string) {
	s.broadcastSessionIdle(sessionID)
	if s.stateDB == nil {
		return
	}
	go func() {
		// platformID empty: the queue head row carries the authoritative
		// platform, and the busy check resolves the session by id.
		s.queueSvc().Flush(context.Background(), "", sessionID)
	}()
}

// broadcastQueueUpdated broadcasts that a session's follow-up queue
// changed (message enqueued or drained), carrying the session's full
// queue so clients apply it directly without a refetch. The messages key
// is always present (an empty queue sends []), so the client can trust it
// as authoritative rather than polling.
func (s *Server) broadcastQueueUpdated(sessionID string) {
	if sessionID == "" {
		return
	}
	// Platform empty: List resolves the session by id across platforms,
	// matching how Flush drains it. A read error just omits messages so
	// the client falls back to a refetch.
	var messages []queuedMessageView
	if msgs, err := s.queueSvc().List("", sessionID); err == nil {
		messages = make([]queuedMessageView, 0, len(msgs))
		for _, m := range msgs {
			messages = append(messages, toQueuedMessageView(m))
		}
	}
	payload, err := json.Marshal(map[string]interface{}{
		"sessionID": sessionID,
		"messages":  messages,
	})
	if err != nil {
		return
	}
	s.broadcastGlobalEvent("ocman.queue.updated", payload)
}
