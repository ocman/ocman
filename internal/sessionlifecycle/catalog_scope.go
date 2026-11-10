package sessionlifecycle

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

func (m *Module) catalogKind(ctx context.Context, selected string) (platforms.ID, error) {
	if m.d.Sessions != nil {
		if resolver, ok := m.d.Sessions().(interface {
			PlatformKind(context.Context, string) (platforms.ID, error)
		}); ok {
			return resolver.PlatformKind(ctx, selected)
		}
	}
	return platforms.ID(selected).Base(), nil
}

func connectionCooldown(sessions Sessions, selected, provider string) time.Time {
	if scoped, ok := sessions.(interface {
		ConnectionCooldownUntil(string, string) time.Time
	}); ok {
		return scoped.ConnectionCooldownUntil(selected, provider)
	}
	return sessions.CooldownUntil(provider)
}
