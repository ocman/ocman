package queuemod

import (
	"context"
	"sync"

	"github.com/ocman/ocman/internal/state"
)

type projectModelCache struct {
	mu     sync.Mutex
	byRoot map[string]state.ProjectSettings
}

// ForgetProjectModels runs after the shared project-settings route writes.
func (s *Module) ForgetProjectModels(dir string) {
	c := &s.projectModels
	c.mu.Lock()
	delete(c.byRoot, state.ProjectRootForDirectory(dir))
	c.mu.Unlock()
}

func (s *Module) ProjectModelList(ctx context.Context, dir string) ([]string, bool) {
	if s.d.Store == nil {
		return nil, false
	}
	root := state.ProjectRootForDirectory(dir)
	c := &s.projectModels
	// ponytail: lock spans the rare miss read so invalidation cannot be
	// overwritten by stale settings. Split locks only if misses contend.
	c.mu.Lock()
	defer c.mu.Unlock()
	ps, ok := c.byRoot[root]
	if !ok {
		var err error
		if ps, err = s.d.Store.GetProjectSettings(ctx, root); err != nil {
			return nil, false
		}
		if c.byRoot == nil {
			c.byRoot = map[string]state.ProjectSettings{}
		}
		c.byRoot[root] = ps
	}
	return ps.Models, ps.Off
}
