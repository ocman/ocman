package claudecode

import (
	"context"
	"fmt"
	"strings"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

// ReloadIdle never substitutes browsing status for an unreadable project.
func (p *claudePlatform) ReloadIdle(ctx context.Context) (bool, error) {
	sessions, err := p.Adapter.Sessions(ctx, "", 0)
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if platforms.ReloadSessionActive(session) {
			return false, nil
		}
	}
	c := p.projects
	c.mu.Lock()
	adapters := make(map[string]*provider.Adapter, len(c.roots))
	for root, project := range c.roots {
		adapters[root] = project.adapter
	}
	replacing := len(c.draining) != 0
	c.mu.Unlock()
	if replacing {
		return false, fmt.Errorf("claude project supervisor recovery is still draining")
	}
	// Recovery attaches asynchronously. Read persisted supervisors too, rather
	// than treating an absent attachment as proof that its process is idle.
	if c.mgr != nil && c.mgr.State != nil && c.mgr.Runtime != nil {
		roots, err := c.mgr.State.ListManagedProviders(ctx, c.mgr.Runtime.PlatformID)
		if err != nil {
			return false, err
		}
		for _, root := range roots {
			if adapters[root] != nil {
				continue
			}
			row, exists, err := c.mgr.State.GetManagedProvider(ctx, c.mgr.Runtime.PlatformID, root)
			if err != nil {
				return false, err
			}
			if !exists {
				continue
			}
			a, err := provider.New(ctx, strings.TrimPrefix(row.Endpoint, "unix://"))
			if err != nil {
				inst := &ocruntime.Instance{Endpoint: row.Endpoint, Kind: row.Kind, ID: row.RuntimeID, PID: row.PID, RepoRoot: root}
				alive, aliveErr := c.mgr.Runtime.Alive(ctx, inst)
				if aliveErr == nil && !alive {
					continue
				}
				return false, fmt.Errorf("reading Claude supervisor for %s: %w", root, err)
			}
			defer a.Close()
			adapters[root] = a
		}
	}
	for root, adapter := range adapters {
		live, err := adapter.LiveSessions(ctx)
		if err != nil {
			return false, fmt.Errorf("reading Claude supervisor for %s: %w", root, err)
		}
		for _, session := range live {
			if platforms.ReloadSessionActive(session) {
				return false, nil
			}
		}
	}
	return true, nil
}
