package server

import (
	"context"
)

// factoryAttemptID tags one session detail the way applySessionState tags the
// list. Lookup failures must not be mistaken for an untagged session.
func (s *Server) factoryAttemptID(ctx context.Context, platform, sessionID, parentID string) (string, error) {
	if s.stateDB == nil {
		return "", nil
	}
	tags, err := s.stateDB.FactorySessions(ctx)
	if err != nil {
		return "", err
	}
	if attemptID := s.sessionAssociationID(tags, platform, sessionID); attemptID != "" {
		return attemptID, nil
	}
	return s.sessionAssociationID(tags, platform, parentID), nil
}
