package server

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// Catalogue identities name the native kind; MCP responses and subsequent
// operations must retain the exact account registration key.
func (s sessionMCPService) identityPlatform(ctx context.Context, info *platforms.SessionIdentity) string {
	if _, key, ok := s.server.registry.PlatformForCanonicalSource(ctx, info.OwnerID, info.Platform, info.SourceKey); ok {
		return string(key)
	}
	return info.Platform
}

func (s sessionMCPService) enrichDetail(ctx context.Context, adapter platforms.Platform, id string, detail *platforms.SessionDetail) {
	key := string(adapter.ID())
	if registered, ok := s.server.registry.KeyFor(adapter); ok {
		key = string(registered)
	}
	s.server.enrichSessionDetail(ctx, string(adapter.ID()), id, detail, !isRemotePlatformID(key))
	s.server.enrichSessionCreator(ctx, key, id, detail)
	detail.Session.Platform = key
	for i := range detail.SessionTree {
		detail.SessionTree[i].Platform = key
	}
}
