package server

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/worker"
)

// queueSweepInterval is a recovery backstop for rows whose idle event was
// missed, including rows left by a crash. Idle events enqueue a flush now.
const queueSweepInterval = time.Minute

type queueFlush struct {
	platformID string
	sessionID  string
	cooled     string // provider that just cooled under the session; continue it
}

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
			&sessionStatusReader{s: s},
			func(ctx context.Context, platform, sessionID string) {
				s.broadcastQueueUpdated(ctx, platform, sessionID)
			},
		)
	})
	return s.queueSvcCached
}

func (s *Server) queueFlushWorker() *worker.Worker[queueFlush] {
	s.queueWorkerOnce.Do(func() {
		s.queueWorker = worker.NewKeyed(func(item queueFlush) {
			runWithRecover("queue-flush", func() {
				if s.continueSession(context.Background(), item.platformID, item.sessionID, item.cooled) {
					return
				}
				s.queueSvc().Flush(context.Background(), item.platformID, item.sessionID)
			})
		}, func(item queueFlush) string { return item.platformID + "\x00" + item.sessionID })
	})
	return s.queueWorker
}

// queueSender implements queuesvc.Sender by forwarding to sessionsvc's
// direct-send path. The composer's own "send now" path calls sendNow
// directly rather than through the queue, so there is no recursion.
type queueSender struct{ s *Server }

type sessionStatusReader struct{ s *Server }

// lifecycle makes one owner-routed read of the session's settled status and
// latest message. It uses the bounded LifecycleReader so the cost does not
// grow with the transcript; Session(id, 1, 0) limits only the returned page.
func (i *sessionStatusReader) lifecycle(ctx context.Context, platform, sessionID string) (*platforms.SessionLifecycle, bool) {
	p, found := i.s.adapterForSession(ctx, platform, sessionID)
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

func (i *sessionStatusReader) TurnRunning(ctx context.Context, platform, sessionID string) (bool, bool) {
	l, ok := i.lifecycle(ctx, platform, sessionID)
	if !ok {
		return false, false
	}
	return l.Status == db.StatusBusy, true
}

func (i *sessionStatusReader) LatestMessageState(ctx context.Context, platform, sessionID string) (string, int64, bool, bool, bool) {
	l, ok := i.lifecycle(ctx, platform, sessionID)
	if !ok {
		return "", 0, false, false, false
	}
	running := l.Status == db.StatusBusy
	if l.LatestMessageID == "" {
		return "", 0, running, !running, true
	}
	return l.LatestMessageID, l.LatestMessageCreated, running, l.LatestMessageRole == "assistant" && !running, true
}

func (q *queueSender) SendNow(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	return q.s.sendHeld(ctx, platformID, req)
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
// EnsureProjectOpencode on the host that owns the session's adapter
// (probe-reuse makes it a no-op when the instance is actually healthy).
// The owner comes from the adapter's compound platform id, never from
// directory inference: the same path can exist on several machines, and
// a missing or stale inventory would relaunch on the wrong one. A
// disconnected owner fails closed. Returns whether the instance is now
// usable. Soft-fail: any resolution or launch error returns false and
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
	owner, _ := remote.SplitPlatformID(string(adapter.ID()))
	host, ok := s.router().LookupRemote(owner)
	if !ok {
		log.WithFields(log.Fields{"sessionID": sessionID, "remoteId": owner}).
			Warn("not relaunching opencode: session owner is not connected")
		return false
	}
	res, err := host.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
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
// serial worker so a slow platform send can't stall the SSE watcher and tests
// can wait for all queued flushes with Drain.
//
// platformID names the instance the edge came from. It is required: a bare
// session id is not an identity, and flushing on one let an idle edge from
// this machine drain a remote session that happened to share the id.
func (s *Server) onSessionIdle(platformID, sessionID string) {
	s.broadcastSessionIdle(sessionID)
	if notifier, ok := s.factory.(interface{ NotifySessionIdle(string, string) }); ok {
		notifier.NotifySessionIdle(platformID, sessionID)
	}
	if s.stateDB == nil || platformID == "" {
		return
	}
	// Synchronously, before the flush is enqueued: a held message must
	// see the dead provider's cooldown when it drains.
	cooled := s.recordQuotaCooldown(context.Background(), platformID, sessionID)
	if p, ok := s.fallAborted.LoadAndDelete(fallKey(platformID, sessionID)); ok && cooled == "" {
		cooled = p.(string)
	}
	s.queueFlushWorker().Enqueue(queueFlush{platformID: platformID, sessionID: sessionID, cooled: cooled})
	go runWithRecover("plugin-conversation-reply", func() {
		s.replyToConversation(context.Background(), platformID, sessionID)
	})
	go runWithRecover("share-relay-publish", func() {
		adapter, ok := s.adapterForSession(context.Background(), "", sessionID)
		if !ok {
			return
		}
		if err := s.publishCompletedTurn(context.Background(), adapter, sessionID); err != nil {
			log.WithError(err).WithField("session_id", sessionID).Warn("publishing completed turn to share relay")
		}
	})
}

// broadcastQueueUpdated broadcasts that a session's follow-up queue
// changed (message enqueued or drained), carrying the session's full
// queue so clients apply it directly without a refetch. The messages key
// is always present (an empty queue sends []), so the client can trust it
// as authoritative rather than polling.
func (s *Server) broadcastQueueUpdated(ctx context.Context, platform, sessionID string) {
	if sessionID == "" || platform == "" {
		return
	}
	// Scoped to the owning platform: a same-id session on another machine
	// has its own queue and its own broadcast. A read error just omits
	// messages so the client falls back to a refetch.
	var messages []queuedMessageView
	if msgs, err := s.queueSvc().List(ctx, platform, sessionID); err == nil {
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
	s.broadcastGlobalEvent("ocman.queue.updated", payload)
}
