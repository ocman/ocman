package local

import (
	"context"
	"fmt"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (h *Host) EnsureRuntime(ctx context.Context, platform, dir string) (*hostsvc.EnsureRuntimeResult, error) {
	if h.deps.PlatformEnabled != nil && !h.deps.PlatformEnabled(platform) {
		return nil, fmt.Errorf("platform %q is disabled", platform)
	}
	if ensure := h.deps.Runtimes[platform]; ensure != nil {
		return ensure(ctx, dir)
	}
	if platform != string(platforms.OpenCodeID) {
		return nil, fmt.Errorf("runtime for platform %q is unavailable", platform)
	}
	return h.EnsureProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: dir})
}

func (h *Host) opencodeEnabled() bool {
	return h.deps.PlatformEnabled == nil || h.deps.PlatformEnabled(string(platforms.OpenCodeID))
}
