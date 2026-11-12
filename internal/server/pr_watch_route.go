package server

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/identity"
)

// Saved PR links use the native kind for session metadata. Execution needs the
// immutable connection key, which can differ even for newly linked sessions.
func (s *Server) prWatchRoute(ctx context.Context, scope, id string) (string, error) {
	if !s.registry.CanonicalSessionIDs() || !identity.IsSessionID(id) {
		return scope, nil
	}
	info, err := s.sessions.LookupSessionReference(ctx, "", id)
	if err != nil {
		return "", err
	}
	_, kind := platforms.SplitPlatformID(info.Platform)
	_, key, ok := s.registry.PlatformForCanonicalSource(ctx, info.OwnerID, kind, info.SourceKey)
	if !ok || (scope != info.Platform && scope != string(key)) {
		return "", platforms.ErrPlatformUnreachable
	}
	return string(key), nil
}
