package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
)

// Follow-ups (Ctrl/Cmd+Enter) prefer the platform's own queue when it
// has one: OpenCode v2 holds them in the session inbox and delivers one
// per idle boundary itself. Platforms without one (OpenCode v1, an
// unreachable instance) go to ocman's queue (#58) instead. Both kinds
// are listed together, so the composer shows one queue.

// nativeQueueTimeout bounds a native queue lookup on the queue paths.
const nativeQueueTimeout = 3 * time.Second

// mergeQueued lists held follow-ups in delivery order: the platform's
// own queue drains first (ocman's items join its back, see sendHeld),
// then ocman's queue in its user-chosen order.
func mergeQueued(ocman, native []queuedMessageView) []queuedMessageView {
	return append(append([]queuedMessageView{}, native...), ocman...)
}

// sendHeld delivers one message from ocman's queue. While the platform
// still holds older follow-ups natively, it joins the back of that queue
// instead of being sent at once, so it cannot overtake them.
func (s *Server) sendHeld(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	if len(s.nativeQueuedViews(ctx, platformID, req.SessionID)) > 0 {
		native := req
		native.Delivery = "queue"
		err := s.sessions.SendMessage(ctx, platformID, native)
		if !errors.Is(err, platforms.ErrUnsupported) {
			return err
		}
		// The selection cannot be changed while older native inputs remain.
		// Keep this head for the next idle edge without spending retries.
		return queuesvc.ErrDeferred
	}
	return s.sendNow(ctx, platformID, req)
}

// enqueueFollowUp holds send for the session's next idle edge.
func (s *Server) enqueueFollowUp(ctx context.Context, platformID string, send platforms.SendMessageRequest) error {
	// Messages already held by ocman (queued while the platform was
	// unreachable) go first: a native follow-up would overtake them.
	if held, err := s.queueSvc().List(ctx, platformID, send.SessionID); err == nil && len(held) > 0 {
		return s.queueSvc().Enqueue(ctx, platformID, true, send)
	}
	if nq := s.nativeQueue(ctx, platformID, send.SessionID); nq != nil {
		// Listing doubles as the capability probe: it answers
		// ErrUnsupported unless the session's server holds follow-ups.
		if _, err := nq.NativeQueued(ctx, send.SessionID); err == nil {
			native := send
			native.Delivery = "queue"
			err := s.sessions.SendMessage(ctx, platformID, native)
			if err == nil {
				s.broadcastQueueUpdated(ctx, platformID, send.SessionID)
				return nil
			}
			// Only a definitive "no native queue" falls back. Any other
			// failure (a lost remote response reads as unreachable) may
			// have been accepted already; queuing again could deliver the
			// prompt twice, so it is surfaced instead.
			if !errors.Is(err, platforms.ErrUnsupported) {
				var upstream *platforms.UpstreamError
				if !errors.As(err, &upstream) {
					// HTTP 5xx is automatically replayed by the composer. A
					// lost response after native admission has an unknown
					// outcome, so use the non-replayable upstream-error path
					// (REST 422) instead, retaining definite upstream errors.
					return &platforms.UpstreamError{Status: http.StatusConflict,
						Message: "Follow-up delivery could not be confirmed. Check the conversation and queue before retrying."}
				}
				return err
			}
		}
	}
	return s.queueSvc().Enqueue(ctx, platformID, true, send)
}

// nativeQueue returns the session's adapter when it can hold follow-ups.
func (s *Server) nativeQueue(ctx context.Context, platformID, sessionID string) platforms.NativeQueue {
	adapter, ok := s.adapterForSession(ctx, platformID, sessionID)
	if !ok {
		return nil
	}
	nq, _ := adapter.(platforms.NativeQueue)
	return nq
}

// nativeQueuedViews lists platform-held follow-ups; nil when there are
// none or the platform keeps no queue.
func (s *Server) nativeQueuedViews(ctx context.Context, platformID, sessionID string) []queuedMessageView {
	nq := s.nativeQueue(ctx, platformID, sessionID)
	if nq == nil {
		return nil
	}
	// Runs inside queue notifications, under the session's queue lock.
	ctx, cancel := context.WithTimeout(ctx, nativeQueueTimeout)
	defer cancel()
	msgs, err := nq.NativeQueued(ctx, sessionID)
	if err != nil {
		if !errors.Is(err, platforms.ErrUnsupported) {
			log.WithError(err).WithField("session", sessionID).Debug("listing native follow-ups")
		}
		return nil
	}
	out := make([]queuedMessageView, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, queuedMessageView{ID: m.ID, Text: m.Text, HasImages: m.HasImages, CreatedAt: m.CreatedAt})
	}
	return out
}

// cancelNativeQueued drops a platform-held follow-up. handled is false
// when id is not a native item (ocman queue ids never use the prefix).
func (s *Server) cancelNativeQueued(ctx context.Context, platformID, sessionID, id string) (handled bool, err error) {
	if !strings.HasPrefix(id, "msg_") {
		return false, nil
	}
	nq := s.nativeQueue(ctx, platformID, sessionID)
	if nq == nil {
		return false, nil
	}
	if err := nq.CancelNativeQueued(ctx, platforms.CancelNativeQueuedRequest{SessionID: sessionID, ID: id}); err != nil {
		return true, err
	}
	s.broadcastQueueUpdated(ctx, platformID, sessionID)
	return true, nil
}
