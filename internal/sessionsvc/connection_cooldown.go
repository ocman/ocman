package sessionsvc

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// RecordSessionCooldown records quota exhaustion only for the session's exact
// account. Unknown named accounts never acquire global cooldowns.
func (s *Service) RecordSessionCooldown(ctx context.Context, sessionID, provider string, d time.Duration) {
	if s.connections == nil {
		s.RecordCooldown(ctx, provider, d)
		return
	}
	if id := s.cooldownConnection(ctx, sessionID); id != "" && provider != "" {
		s.recordCooldownKey(ctx, connectionCooldownKey(id, provider), d)
	}
}

// SessionCooldownUntil returns the active cooldown for the session's account.
// Legacy services retain their provider-wide cooldown behavior.
func (s *Service) SessionCooldownUntil(ctx context.Context, sessionID, provider string) time.Time {
	if s.connections == nil {
		return s.CooldownUntil(provider)
	}
	return s.ConnectionCooldownUntil(s.cooldownConnection(ctx, sessionID), provider)
}

// ConnectionCooldownUntil accepts an exact local connection ID or an
// owner-qualified remote registry key. Empty named scopes have no cooldown.
func (s *Service) ConnectionCooldownUntil(connectionID, provider string) time.Time {
	if s.connections == nil {
		return s.CooldownUntil(provider)
	}
	if connectionID == "" || provider == "" {
		return time.Time{}
	}
	return s.cooldowns.active(connectionCooldownKey(connectionID, provider))
}

func connectionCooldownKey(connectionID, provider string) string {
	return connectionID + "\x00" + provider
}

func (s *Service) cooldownConnection(ctx context.Context, sessionID string) string {
	row, err := s.connections.catalogue.GetCanonical(ctx, sessionID)
	if err != nil || row.Purged {
		return ""
	}
	if row.OwnerID == s.connections.owner {
		route, err := s.connections.router.ResolveSession(ctx, sessionID)
		if err != nil {
			return ""
		}
		return route.Connection.ID
	}
	// OwnerPlatform authenticates the recorded owner and exact physical source.
	// Require its registry key rather than guessing from a kind or default.
	sources, ok := s.registry.(interface {
		PlatformForCanonicalSource(context.Context, string, string, string) (platforms.Platform, platforms.ID, bool)
	})
	if !ok {
		return ""
	}
	_, expected, found := sources.PlatformForCanonicalSource(ctx, row.OwnerID, row.Platform, row.SourceKey)
	if !found {
		return ""
	}
	p, err := s.identities.OwnerPlatform(ctx, sessionID, "")
	if err != nil {
		return ""
	}
	registry, ok := s.registry.(interface {
		KeyFor(platforms.Platform) (platforms.ID, bool)
	})
	if !ok {
		return ""
	}
	key, found := registry.KeyFor(p)
	owner, _ := platforms.SplitPlatformID(string(key))
	if !found || key != expected || owner != row.OwnerID {
		return ""
	}
	return string(key)
}
