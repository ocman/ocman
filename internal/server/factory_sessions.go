package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/state"
)

// factoryAttemptID tags one session detail the way applySessionState tags the
// list. A lookup failure only hides the Factory recovery card.
func (s *Server) factoryAttemptID(ctx context.Context, platform, sessionID string) string {
	if s.stateDB == nil {
		return ""
	}
	tags, err := s.stateDB.FactorySessions(ctx)
	if err != nil {
		return ""
	}
	return tags[state.Key{Platform: platform, SessionID: sessionID}]
}
