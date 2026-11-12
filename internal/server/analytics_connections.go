package server

import (
	"context"

	"github.com/ocman/ocman/internal/analytics"
	"github.com/ocman/ocman/internal/platformconnections"
	"github.com/ocman/ocman/internal/platforms"
)

// Subscription reporting remains owner-local. Resolve names and enabled intent
// at read time, rather than caching mutable configuration with account quotas.
func (s *Server) subscriptionSources(ctx context.Context) ([]analytics.SubscriptionSource, error) {
	if s.registry == nil {
		return nil, nil
	}
	var named map[string]platformconnections.Connection
	if s.connectionStore != nil {
		collection, err := s.connectionStore.Read(ctx)
		if err != nil {
			return nil, err
		}
		named = make(map[string]platformconnections.Connection, len(collection.Connections))
		for _, c := range collection.Connections {
			named[c.ID] = c
		}
	}
	var sources []analytics.SubscriptionSource
	for _, entry := range s.registry.Entries() {
		if owner, _ := platforms.SplitPlatformID(string(entry.Key)); owner != "" {
			continue
		}
		name := entry.Platform.DisplayName()
		if named != nil {
			c, ok := named[string(entry.Key)]
			if !ok || !c.Enabled || c.Removed {
				continue
			}
			name = c.Name
		}
		sources = append(sources, analytics.SubscriptionSource{Key: entry.Key, Name: name, Platform: entry.Platform})
	}
	return sources, nil
}
