package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
	sweep := func() { runWithRecover("queue-sweep", func() { s.queueSvc().Sweep(ctx) }) }
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

// queueServiceFn builds the queue Service lazily, so tests can override
// it. nil means "construct the production service".
var queueServiceFn func(s *Server) *queuesvc.Service

// queueSvc returns the follow-up message queue service, building it on
// first use.
func (s *Server) queueSvc() *queuesvc.Service {
	s.queueSvcOnce.Do(func() {
		if queueServiceFn != nil {
			s.queueSvcCached = queueServiceFn(s)
			return
		}
		s.queueSvcCached = queuesvc.New(
			s.stateDB,
			&queueSender{s: s},
			&workflowStatusInferer{s: s},
			func(sessionID string) { s.broadcastQueueUpdated(sessionID) },
		)
	})
	return s.queueSvcCached
}

// queueSender implements queuesvc.Sender by forwarding to sessionsvc's
// direct-send path. The composer's own "send now" path calls sendNow
// directly rather than through the queue, so there is no recursion.
type queueSender struct{ s *Server }

func (q *queueSender) SendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	return q.s.sendNow(ctx, platformID, req)
}

// sendNow delivers a message to the platform immediately, retrying once
// behind a relaunch when the session's opencode instance is stale/gone.
// Shared by the queue drain and by the composer's explicit "send now"
// path (an Enter send, which interleaves into a running turn).
func (s *Server) sendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	err := s.sessions.SendMessage(ctx, platformID, req)
	if err == nil || !errors.Is(err, platforms.ErrPlatformUnreachable) {
		return err
	}
	// The session's opencode instance is stale/gone. Relaunch the
	// project's single instance and retry the send once. On failure the
	// queued message stays at the head, so the next idle edge or sweep
	// retries — relaunch included; a direct send surfaces the error.
	if !s.relaunchOpencodeForSession(ctx, platformID, req.SessionID) {
		return err
	}
	return s.sessions.SendMessage(ctx, platformID, req)
}

// relaunchOpencodeForSession resolves the session's project root and runs
// EnsureProjectOpencode through the owning host (ForDir — a remote
// session relaunches on its own machine; probe-reuse makes it a no-op
// when the instance is actually healthy). Returns whether the instance is
// now usable. Soft-fail: any resolution or launch error returns false and
// leaves the caller's original error intact.
func (s *Server) relaunchOpencodeForSession(ctx context.Context, platformID, sessionID string) bool {
	adapter, ok := s.adapterForSession(ctx, platformID, sessionID)
	if !ok {
		return false
	}
	detail, err := adapter.Session(ctx, sessionID, 0, 0)
	if err != nil || detail == nil || detail.Session == nil || detail.Session.Directory == "" {
		return false
	}
	// Worktree sessions run on the project's shared instance rooted at
	// the main checkout; fold the worktree path back to it.
	dir := projectRootForDirectory(detail.Session.Directory)
	res, err := s.router().ForDir(dir).EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"sessionID": sessionID, "directory": dir}).
			Warn("relaunching opencode for unreachable session")
		return false
	}
	if res.Launched {
		log.WithFields(log.Fields{"sessionID": sessionID, "directory": dir, "endpoint": res.Endpoint}).
			Info("relaunched opencode for unreachable session")
	}
	return true
}

// adapterForSession mirrors sessionsvc's resolution order: an explicit
// platform ID wins (AD-2b), else the registry's reverse lookup.
func (s *Server) adapterForSession(ctx context.Context, platformID, sessionID string) (platforms.Platform, bool) {
	if platformID != "" {
		return s.registry.Get(platforms.ID(platformID))
	}
	return s.registry.PlatformForSession(ctx, sessionID)
}

// onSessionIdle handles the session.idle edge: it broadcasts idle (as
// before) and drains the session's follow-up queue. The flush is the
// authoritative send gate for held messages (#58) — a Ctrl+Enter enqueue
// never sends directly, so the idle edge is what delivers it. Runs in its
// own goroutine so a slow platform send can't stall the SSE watcher.
func (s *Server) onSessionIdle(sessionID string) {
	s.broadcastSessionIdle(sessionID)
	if s.stateDB == nil {
		return
	}
	go runWithRecover("queue-flush", func() {
		// platformID empty: the queue head row carries the authoritative
		// platform, and the busy check resolves the session by id.
		s.queueSvc().Flush(context.Background(), "", sessionID)
	})
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
