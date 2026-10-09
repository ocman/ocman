package server

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
	log "github.com/sirupsen/logrus"
)

func (s *Server) recordAgentWait(update func(context.Context) error) {
	if s.stateDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := update(ctx); err != nil {
		log.WithError(err).Warn("recording agent user wait")
	}
}

func (s *Server) onPromptNeedsUser(platform, session, kind, request string) {
	if (kind == "permission" || kind == "question") && session != "" && request != "" {
		s.recordAgentWait(func(ctx context.Context) error {
			return s.stateDB.StartAgentUserWait(ctx, platform, session, kind, request, time.Now().UnixMilli())
		})
	}
	s.conversationPromptNeedsUser(platform, session, kind, request)
}

func (s *Server) onAgentPromptResolved(session, kind, request string) {
	s.recordAgentWait(func(ctx context.Context) error {
		return s.stateDB.ResolveAgentUserWait(ctx, string(opencode.PlatformID), session, kind, request, time.Now().UnixMilli())
	})
}

func (s *Server) onAgentWaitIdle(platform, session string) {
	s.recordAgentWait(func(ctx context.Context) error {
		return s.stateDB.ResolveSessionUserWaits(ctx, platform, session, time.Now().UnixMilli())
	})
}
