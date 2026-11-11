package server

import (
	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/platforms"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
)

// Legacy judge selection follows the durable migrated-account binding. Never
// choose an arbitrary named account, or substitute one for an unavailable owner.
func (s *Server) legacyOpenCodePlatform() platforms.Platform {
	if s.registry == nil {
		return nil
	}
	key, err := s.registry.ResolveLegacyScope(s.lifetime(), string(opencode.PlatformID))
	if err != nil {
		return nil
	}
	adapter, _ := s.registry.Get(key)
	return adapter
}

func (s *Server) openCodeWatchers() []autoapprove.WatcherConfig {
	var configs []autoapprove.WatcherConfig
	if s.registry == nil {
		return configs
	}
	for _, entry := range s.registry.Entries() {
		adapter := entry.Platform
		if !isLocalAdapter(adapter) || adapter.ID() != opencode.PlatformID || platforms.OpenCodeObserver(adapter) == nil {
			continue
		}
		provider, ok := adapter.(platforms.OpenCodeEventClientProvider)
		if !ok {
			continue
		}
		client := provider.OpenCodeEventClient()
		if client == nil {
			continue
		}
		configs = append(configs, autoapprove.WatcherConfig{
			Platform: entry.Key,
			Engine:   s.autoapproveEngineDepsFor(entry.Key, func() platforms.Platform { return adapter }),
			Client:   client,
		})
	}
	return configs
}
