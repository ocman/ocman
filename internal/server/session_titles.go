package server

import (
	"context"
	"encoding/json"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessiontitles"
)

func (s *Server) firstPromptAdded(ctx context.Context, e module.FirstPromptAdded) {
	s.bus().PublishFirstPromptAdded(ctx, e)
}

func (s *Server) sessionTitlesModule() *sessiontitles.Module {
	return s.sessionTitles.get(func() *sessiontitles.Module {
		return sessiontitles.NewModule(sessiontitles.Deps{
			Lookup: func(id string) (platforms.Platform, bool) {
				if s.registry == nil {
					return nil, false
				}
				return s.registry.Get(platforms.ID(id))
			},
			Rename: func(ctx context.Context, id string, req platforms.RenameSessionRequest) error {
				return s.sessions.Rename(ctx, id, req)
			},
			Changed: func(platform, id string) {
				payload, _ := json.Marshal(map[string]string{"platform": platform, "sessionID": id})
				s.broadcastGlobalEvent("ocman.session.changed", payload)
			},
		})
	})
}
