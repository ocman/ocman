package server

import (
	"context"
	"fmt"
)

// Routines share the composer's owner/account selection, including named
// connection defaults. Never fall back from a disconnected explicit owner.
func (s *Server) routinePlatform(ctx context.Context, remoteID, requested, model string) (string, error) {
	host, ok := s.router().LookupRemote(remoteID)
	if !ok {
		return "", fmt.Errorf("routine target %q is unavailable", remoteID)
	}
	id, _, err := s.createPlatform(ctx, host, requested, model)
	if err != nil {
		return "", err
	}
	if _, err := s.sessions.ResolvePlatformID(ctx, id); err != nil {
		return "", err
	}
	return id, nil
}
