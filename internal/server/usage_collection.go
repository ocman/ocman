package server

import (
	"context"
	"time"

	"github.com/ocman/ocman/internal/convstream"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/usagecollection"
	"github.com/ocman/ocman/internal/usagewire"
)

func healthPlatform(kind string) string {
	if kind == "claudecode" {
		return "claude"
	}
	return kind
}

func (s *Server) recordHealthStart(adapter platforms.Platform, err error) {
	if !isRemotePlatformID(string(adapter.ID())) {
		s.usageCollectionModule().RecordStart(s.lifetime(), healthPlatform(eventNativeKind(adapter)), err)
	}
}

func (s *Server) beginHealthCancel(adapter platforms.Platform, sessionID string) func(bool) {
	return s.convStreamModule().Hub().BeginHealthCancel(convstream.Key{Platform: string(adapter.ID()), SessionID: sessionID})
}

func (s *Server) beginHealthTurn(key convstream.Key) func(string, time.Duration, bool) {
	if isRemotePlatformID(key.Platform) {
		return nil // Do not bypass a remote owner's reporting preference.
	}
	adapter, ok := s.registry.Get(platforms.ID(key.Platform))
	if !ok {
		return nil
	}
	return s.usageCollectionModule().BeginHealthTurn(s.lifetime(), healthPlatform(eventNativeKind(adapter)))
}

func (s *Server) usageCollectionModule() *usagecollection.Module {
	return s.usageCollectionMod.get(func() *usagecollection.Module {
		return usagecollection.NewModule(usagecollection.Deps{
			Store:    s.stateDB,
			RelayURL: func() string { return s.relayURL },
			Version:  func() string { return s.version },
			Daily:    s.productUsageDay,
		})
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
