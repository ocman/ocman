package server

import (
	"context"
	"errors"

	log "github.com/sirupsen/logrus"

	"github.com/ocman/ocman/internal/platforms"
)

func (s *Server) enrichSessionCreator(ctx context.Context, platform, sessionID string, detail *platforms.SessionDetail) {
	if s.stateDB == nil {
		return
	}
	// MCP launches are recorded by the launching hub, including remote targets.
	// Use the compound platform key, never another machine's unscoped identity.
	creator, err := s.stateDB.GetSessionCreator(ctx, platform, sessionID)
	if err != nil {
		level := log.WarnLevel
		if errors.Is(err, context.Canceled) {
			level = log.DebugLevel
		}
		log.WithError(err).Log(level, "reading session creator")
		return
	}
	// This is provenance held by the MCP-serving hub, not native owner ancestry.
	if creator != nil {
		detail.Creator = &platforms.SessionCreator{
			Platform: creator.CreatorPlatform, SessionID: creator.CreatorSessionID, CreatedAt: creator.CreatedAt,
		}
		if detail.Session != nil {
			detail.Session.Creator = detail.Creator
		}
	}
}
