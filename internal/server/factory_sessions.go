package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/state"
)

// factoryAttemptID tags one session detail the way applySessionState tags the
// list. A lookup failure only hides the Factory recovery card.
func (s *Server) factoryAttemptID(ctx context.Context, platform, sessionID, parentID string) string {
	if s.stateDB == nil {
		return ""
	}
	tags, err := s.stateDB.FactorySessions(ctx)
	if err != nil {
		return ""
	}
	if attemptID := tags[state.Key{Platform: platform, SessionID: sessionID}]; attemptID != "" {
		return attemptID
	}
	return tags[state.Key{Platform: platform, SessionID: parentID}]
}
