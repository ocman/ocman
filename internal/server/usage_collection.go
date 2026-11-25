package server

import (
	"context"
	"os"
	"time"

	"github.com/ocman/ocman/internal/usagecollection"
	"github.com/ocman/ocman/internal/usagewire"
)

func (s *Server) usageCollectionModule() *usagecollection.Module {
	return s.usageCollectionMod.get(func() *usagecollection.Module {
		deps := usagecollection.Deps{
			Store:    s.stateDB,
			RelayURL: func() string { return s.relayURL },
			Version:  func() string { return s.version },
		}
		if os.Getenv("OCMAN_USAGE_DAILY_MODELS") == "true" {
			deps.Daily = s.productUsageDay
		}
		return usagecollection.NewModule(deps)
	})
}

func (s *Server) productUsageDay(ctx context.Context, day time.Time) (usagewire.Daily, error) {
	history := s.historyAnalytics
	if history == nil || history.Store == nil || history.Instances == nil {
		return usagewire.Daily{}, nil
	}
	ids, ready := history.Instances()
	if !ready {
		return usagewire.Daily{}, nil
	}
	return history.Store.ProductUsageDay(ctx, ids, day)
}
