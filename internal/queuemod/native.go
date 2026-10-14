package queuemod

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
func (s *Module) sendHeld(ctx context.Context, platformID string, req platforms.SendMessageRequest) error {
	send, err := s.PrepareSend(ctx, platformID, req)
	if err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return send(ctx)
}

// PrepareSend completes ordering and shared sender discovery before admission.
func (s *Module) PrepareSend(ctx context.Context, platformID string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	native := len(s.nativeQueuedViews(ctx, platformID, req.SessionID)) > 0
	if native {
		req.Delivery = "queue"
	}
	send, err := s.prepareDirect(ctx, platformID, req)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil, queuesvc.ErrDeferred
	}
	if err != nil {
		return nil, err
	}
	if !native {
		return send, nil
	}
	return func(delivery context.Context) error {
		err := send(delivery)
		if errors.Is(err, platforms.ErrUnsupported) {
			return queuesvc.ErrDeferred
		}
		return err
	}, nil
}

func (s *Module) prepareDirect(ctx context.Context, platformID string, req platforms.SendMessageRequest) (func(context.Context) error, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if s.d.PrepareSend != nil {
		return s.d.PrepareSend(ctx, platformID, req)
	}
	return func(delivery context.Context) error {
		if req.Delivery == "queue" {
			return s.d.Sessions().SendMessage(delivery, platformID, req)
		}
		return s.d.SendNow(delivery, platformID, req)
	}, nil
}

// enqueueFollowUp holds send for the session's next idle edge.
func (s *Module) EnqueueFollowUp(ctx context.Context, platformID string, send platforms.SendMessageRequest) error {
	// A read-only session would accept the hold and fail every flush (FR-4).
	if adapter, ok := s.d.Adapter(ctx, platformID, send.SessionID); ok {
		if reader, ok := adapter.(platforms.LifecycleReader); ok {
			if lc, err := reader.SessionLifecycle(ctx, send.SessionID); err == nil && lc.ReadOnly {
				return &platforms.UpstreamError{Status: http.StatusForbidden, Message: "session is read-only in ocman"}
			}
		}
	}
	// Messages already held by ocman (queued while the platform was
	// unreachable) go first: a native follow-up would overtake them.
	if held, err := s.queue.List(ctx, platformID, send.SessionID); err == nil && len(held) > 0 {
		return s.queue.Enqueue(ctx, platformID, true, send)
	}
	if nq := s.nativeQueue(ctx, platformID, send.SessionID); nq != nil {
		// Listing doubles as the capability probe: it answers
		// ErrUnsupported unless the session's server holds follow-ups.
		if _, err := nq.NativeQueued(ctx, send.SessionID); err == nil {
			native := send
			native.Delivery = "queue"
			err := s.d.Sessions().SendMessage(ctx, platformID, native)
			if err == nil {
				s.BroadcastQueueUpdated(ctx, platformID, send.SessionID)
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
	return s.queue.Enqueue(ctx, platformID, true, send)
}

// nativeQueue returns the session's adapter when it can hold follow-ups.
func (s *Module) nativeQueue(ctx context.Context, platformID, sessionID string) platforms.NativeQueue {
	adapter, ok := s.d.Adapter(ctx, platformID, sessionID)
	if !ok {
		return nil
	}
	nq, _ := adapter.(platforms.NativeQueue)
	return nq
}

// nativeQueuedViews lists platform-held follow-ups; nil when there are
// none or the platform keeps no queue.
func (s *Module) nativeQueuedViews(ctx context.Context, platformID, sessionID string) []queuedMessageView {
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
func (s *Module) cancelNativeQueued(ctx context.Context, platformID, sessionID, id string) (handled bool, err error) {
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
	s.BroadcastQueueUpdated(ctx, platformID, sessionID)
	return true, nil
}
