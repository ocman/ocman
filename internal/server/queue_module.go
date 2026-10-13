package server

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/queuemod"
	"github.com/NoUseFreak/ocman/internal/queuesvc"
)

func (s *Server) queueModule() *queuemod.Module {
	s.queueModOnce.Do(func() {
		d := queuemod.Deps{
			Sessions: func() queuemod.Sessions {
				if s.sessions == nil {
					return nil
				}
				return s.sessions
			},
			Adapter: s.adapterForSession, SendNow: s.sendNow,
			Broadcast: s.broadcastGlobalEvent, SessionChanged: s.broadcastSessionChanged,
			Guards: module.Guards{Auth: s.requireAuth, Localhost: s.requireLocalhost},
		}
		if s.stateDB != nil {
			d.Store = s.stateDB
		}
		s.queueMod = queuemod.NewModule(d)
	})
	return s.queueMod
}

func (s *Server) queueSvc() *queuesvc.Service { return s.queueModule().Service() }
func (s *Server) broadcastQueueUpdated(ctx context.Context, platform, id string) {
	s.queueModule().BroadcastQueueUpdated(ctx, platform, id)
}
func (s *Server) enqueueFollowUp(ctx context.Context, platform string, req platforms.SendMessageRequest) error {
	return s.queueModule().EnqueueFollowUp(ctx, platform, req)
}
func (s *Server) cooldownTimes(ctx context.Context) (time.Duration, time.Duration) {
	return s.queueModule().CooldownTimes(ctx)
}
func (s *Server) onSessionRetry(id string, st autoapprove.SessionStatus) {
	s.queueModule().OnSessionRetry(id, st.Next, st.ActionReason)
}
func (s *Server) applyFallNotice(platform string, sess *db.Session) {
	s.queueModule().ApplyFallNotice(platform, sess)
}

// Quota is recorded and the flush admitted before synchronous bus subscribers.
func (s *Server) onSessionIdle(platform, id string) {
	s.broadcastSessionIdle(id)
	if s.stateDB == nil || platform == "" {
		return
	}
	s.queueModule().OnSessionIdle(platform, id)
	s.bus().PublishSessionIdle(context.Background(), module.SessionIdle{Platform: platform, SessionID: id})
}
