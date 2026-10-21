package server

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/identity"
)

// sessionReferenceScope is the common owner policy for REST bodies, queries and
// MCP arguments. Transports declare reference fields and handle their own errors.
type sessionReferenceScope struct{ platform, remote string }

func newSessionReferenceScope(platform, remote string) sessionReferenceScope {
	if owner, base := platforms.SplitPlatformID(platform); platform != "" && owner == "" && remote != "" && remote != "local" {
		platform = platforms.CompoundPlatformID(remote, base)
	}
	return sessionReferenceScope{platform: platform, remote: remote}
}

func (scope *sessionReferenceScope) resolve(ctx context.Context, s *Server, id string, inheritedParent bool) (string, error) {
	if id == "" {
		return "", nil
	}
	if identity.IsPendingReference(id) {
		return "", platforms.ErrPlatformUnreachable
	}
	platform := scope.platform
	// Permission inheritance names a source, not the new session's target.
	if inheritedParent {
		platform = ""
	}
	if !strings.HasPrefix(id, "ocm_") && (s == nil || s.sessions == nil || !s.sessions.CanonicalSessionReferences(platform)) {
		return id, nil
	}
	info, err := s.sessions.LookupSessionReference(ctx, platform, id)
	if err != nil {
		return "", err
	}
	if !inheritedParent {
		owner, _ := platforms.SplitPlatformID(info.Platform)
		if scope.remote == "local" && owner != "" || scope.remote != "" && scope.remote != "local" && owner != scope.remote {
			return "", platforms.ErrPlatformUnreachable
		}
		scope.platform = info.Platform
	}
	return s.sessions.CoreSessionReference(info), nil
}
