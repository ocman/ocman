package claudecode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

// A failed API probe is not proof of absence. Only the tracked process's
// independent liveness check can prove that a supervisor cannot own a running
// child anymore. This keeps dead-runtime recovery without guessing on timeouts.
func (c *claudeProjects) runtimeDefinitelyStopped(ctx context.Context, adapter *provider.Adapter) bool {
	if c.mgr == nil || c.mgr.State == nil || c.mgr.Runtime == nil {
		return false
	}
	c.mu.Lock()
	var root, endpoint string
	for key, project := range c.roots {
		if project.adapter == adapter {
			root, endpoint = key, project.endpoint
			break
		}
	}
	c.mu.Unlock()
	if endpoint == "" {
		return false
	}
	tracked, found, err := c.mgr.State.GetManagedProvider(ctx, c.mgr.Runtime.PlatformID, root)
	if err != nil || !found || tracked.Endpoint != endpoint {
		return false
	}
	alive, err := c.mgr.Runtime.Alive(ctx, &ocruntime.Instance{Endpoint: tracked.Endpoint, Kind: tracked.Kind, ID: tracked.RuntimeID})
	return err == nil && !alive
}
