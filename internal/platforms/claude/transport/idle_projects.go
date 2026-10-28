package claudecode

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc/local"
	"github.com/ocman/ocman/internal/platforms/claude/provider"
)

func (p *claudePlatform) ProjectRoot(ctx context.Context, directory string) (string, error) {
	return local.ProviderRootForCleanup(ctx, directory)
}

func (p *claudePlatform) HistoricalSessionRoots(context.Context) (map[string]string, error) {
	return claudeprovider.OwnedProjectRoots(p.projects.statePath)
}

func (p *claudePlatform) ManagedIdleProjects(ctx context.Context) ([]string, error) {
	if p.projects.mgr.State == nil {
		return nil, nil
	}
	return p.projects.mgr.State.ListManagedProviders(ctx, p.projects.mgr.Runtime.PlatformID)
}

func (p *claudePlatform) CullIdleProject(ctx context.Context, root string, check func(context.Context) (bool, error)) (bool, error) {
	return p.projects.mgr.CullIdle(ctx, root, check, func() {
		c := p.projects
		c.mu.Lock()
		if project := c.roots[root]; project != nil {
			if project.stop != nil {
				project.stop()
			}
			project.adapter.Close()
			delete(c.roots, root)
		}
		c.endDrainLocked(root)
		close(c.attached)
		c.attached = make(chan struct{})
		c.mu.Unlock()
	})
}
