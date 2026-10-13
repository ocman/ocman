package local

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (h *Host) hookPlugin() hostsvc.HookPluginManager {
	if h.deps.HookPlugin == nil {
		return nil
	}
	return h.deps.HookPlugin()
}

func (h *Host) HookPlugin() (bool, bool) {
	if plugin := h.hookPlugin(); plugin != nil {
		return plugin.HookPlugin()
	}
	return false, false
}

func (h *Host) InstallHookPlugin(ctx context.Context) error {
	if plugin := h.hookPlugin(); plugin != nil {
		return plugin.InstallHookPlugin(ctx)
	}
	return platforms.ErrUnsupported
}

func (h *Host) UninstallHookPlugin(ctx context.Context) error {
	if plugin := h.hookPlugin(); plugin != nil {
		return plugin.UninstallHookPlugin(ctx)
	}
	return platforms.ErrUnsupported
}
