package server

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/analytics"
	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// analyticsModule exists without either database: its routes then answer
// as they did before the extraction (501, 503 or an empty list).
func (s *Server) analyticsModule() *analytics.Module {
	return s.analyticsMod.get(func() *analytics.Module {
		return analytics.NewModule(analytics.Deps{
			Platforms: func() []platforms.Platform {
				if s.registry == nil {
					return nil
				}
				return s.registry.Platforms()
			},
			SubscriptionCredentials: s.subscriptionCredentials,
			HistoryAnalytics:        func() *analytics.HistoryAnalytics { return s.historyAnalytics },
			SessionIdentity:         s.historySessionPublicID,
			Store:                   s.stateDB,
			Version:                 func() string { return s.version },
			TypeSafeConfigured: func(ctx context.Context) (bool, error) {
				if s.stateDB == nil {
					return false, nil
				}
				config, err := autoapprove.LoadJudgeEndpoint(ctx, s.stateDB)
				return config.HostedTypeSafe(), err
			},
		})
	})
}
