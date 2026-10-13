package claudecode

import "context"

func (p *claudePlatform) ManagedIdleProjects(ctx context.Context) ([]string, error) {
	if p.projects.mgr.State == nil {
		return nil, nil
	}
	return p.projects.mgr.State.ListManagedProviders(ctx, p.projects.mgr.Runtime.PlatformID)
}

func (p *claudePlatform) CullIdleProject(ctx context.Context, root string, _ []string, check func(context.Context) (bool, error)) (bool, error) {
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
