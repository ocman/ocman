package autoapprove

import (
	"context"
	"net/http"

	"github.com/ocman/ocman/internal/platforms"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
)

// WatcherConfig binds a headless stream to one account's canonical adapter,
// authenticated native transport and owner-qualified publication hooks.
type WatcherConfig struct {
	Platform platforms.ID
	Engine   Deps
	Client   *http.Client
}

func (w *autoApproveWatcher) engine() *Deps {
	if w.scope != nil {
		return &w.scope.Engine
	}
	if w.svc != nil {
		return &w.svc.deps
	}
	return &Deps{}
}

func (w *autoApproveWatcher) platformID() platforms.ID {
	if w.scope != nil {
		return w.scope.Platform
	}
	return opencode.PlatformID
}

func (w *autoApproveWatcher) adapter() platforms.Platform {
	if resolve := w.engine().OpencodePlatform; resolve != nil {
		return resolve()
	}
	return nil
}

func (s *Service) runConnectionWatcher(ctx context.Context, config WatcherConfig) {
	w := newAutoApproveWatcher(s)
	w.scope = &config
	if config.Client != nil {
		w.httpClient = config.Client
	}
	w.discoverPorts = func() map[string]string {
		if provider, ok := w.adapter().(platforms.InstancePortProvider); ok {
			if ports := provider.InstancePorts(); ports != nil {
				return ports.DiscoverOpenCodePorts()
			}
		}
		return nil
	}
	w.markSessionDirty = nil
	if cache, ok := w.adapter().(platforms.SessionCacheMaintainer); ok {
		w.markSessionDirty = cache.MarkSessionDirty
	}
	w.run(ctx)
}
