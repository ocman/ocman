package analytics

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// SubscriptionSource binds account reads to an exact registry connection.
// Key is independent of Platform.ID(), which remains the native tool kind.
type SubscriptionSource struct {
	Key      platforms.ID
	Name     string
	Platform platforms.Platform
}

func (m *Module) subscriptionSources(ctx context.Context) ([]SubscriptionSource, error) {
	if m.deps.SubscriptionSources != nil {
		return m.deps.SubscriptionSources(ctx)
	}
	var sources []SubscriptionSource
	if m.deps.Platforms != nil {
		for _, p := range m.deps.Platforms() {
			sources = append(sources, SubscriptionSource{Key: p.ID(), Name: p.DisplayName(), Platform: p})
		}
	}
	return sources, nil
}

func (s SubscriptionSource) label(value subscriptionProviderUsage) subscriptionProviderUsage {
	value.ID, value.Name = string(s.Key), s.Name
	value.PlatformName = s.Platform.DisplayName()
	value.MachineName = "This machine"
	return value
}
