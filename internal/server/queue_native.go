package server

import (
	"context"
	"errors"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Follow-ups (Ctrl/Cmd+Enter) prefer the platform's own queue when it
// has one: OpenCode v2 holds them in the session inbox and delivers one
// per idle boundary itself. Platforms without one (OpenCode v1, an
// unreachable instance) go to ocman's queue (#58) instead. Both kinds
// are listed together, so the composer shows one queue.

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
			if !errors.Is(err, platforms.ErrUnsupported) && !errors.Is(err, platforms.ErrPlatformUnreachable) {
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
