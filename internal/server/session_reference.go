package server

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
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
	if !strings.HasPrefix(id, "ocm_") {
		return id, nil
	}
	platform := scope.platform
	// Permission inheritance names a source, not the new session's target.
	if inheritedParent {
		platform = ""
	}
	info, err := s.sessions.LookupSessionIdentity(ctx, platform, platforms.SessionIdentityRequest{ID: id})
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
	return info.NativeID, nil
}
