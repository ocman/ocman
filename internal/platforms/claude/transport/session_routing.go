package claudecode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/claude/provider"
	"github.com/ocman/ocman/internal/platforms/provider"
)

// route uses the native session identity, not its mutable working directory.
// A live supervisor's own status is evidence of execution; a shared transcript
// listing or Knows cache is not. Keep a verified binding after the turn ends so
// prompts, subsequent sends and the browser stream keep using the same source.
func (c *claudeProjects) route(ctx context.Context, id string, launch bool) (*provider.Adapter, bool, error) {
	dir, owned := claudeprovider.OwnedDirectory(c.statePath, id)
	if err := ctx.Err(); err != nil {
		return nil, owned, err
	}
	c.mu.Lock()
	bound, root := c.boundOwnerLocked(id)
	c.mu.Unlock()
	if bound != nil {
		if !launch {
			return bound, owned, nil
		}
		// Re-probe before a mutation, retaining dead-supervisor recovery without
		// letting Ensure pick a different project from the session's directory.
		if _, err := bound.LiveSessions(ctx); err == nil {
			return bound, owned, nil
		} else if ctx.Err() != nil {
			return nil, owned, ctx.Err()
		}
		_, a, err := c.ensure(ctx, root)
		if err == nil {
			c.bindSession(id, a)
		}
		return a, owned, err
	}
	a, err := c.findLiveSession(ctx, id)
	if err != nil {
		return nil, owned, err
	}
	if a != nil {
		if owned {
			if err := c.rememberSessionRuntime(id, a); err != nil {
				return nil, owned, err
			}
		}
		c.bindSession(id, a)
		return a, owned, nil
	}
	if owned && launch {
		// A settled session may have no retained live events. Its recorded
		// supervisor is the recovery hint, never its current working directory.
		root, _ := claudeprovider.OwnedProjectRoot(c.statePath, id)
		if root == "" {
			root = dir // legacy ownership records predate projectRoot
		}
		_, a, err := c.ensure(ctx, root)
		if err == nil {
			c.bindSession(id, a)
		}
		return a, true, err
	}
	return c.owner(id), owned, nil
}

func (c *claudeProjects) rememberSessionRuntime(id string, a *provider.Adapter) error {
	c.mu.Lock()
	var root string
	for candidate, p := range c.roots {
		if p.adapter == a {
			root = candidate
			break
		}
	}
	c.mu.Unlock()
	if root == "" {
		return platforms.ErrPlatformUnreachable
	}
	return claudeprovider.RebindSessionRuntime(c.statePath, id, root)
}

func (c *claudeProjects) boundOwnerLocked(id string) (*provider.Adapter, string) {
	if a := c.sessions[id]; a != nil {
		for root, p := range c.roots {
			if p.adapter == a {
				return a, root
			}
		}
		delete(c.sessions, id) // replacement/retirement invalidates the binding
	}
	return nil, ""
}

func (c *claudeProjects) bindSession(id string, a *provider.Adapter) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.sessions[id] == a {
		return
	}
	if c.sessions == nil {
		c.sessions = make(map[string]*provider.Adapter)
	}
	c.sessions[id] = a
	// Existing streams must reconnect when discovery corrects their source.
	if changed := c.sessionChanges[id]; changed != nil {
		close(changed)
		delete(c.sessionChanges, id)
	}
}

func (c *claudeProjects) sessionChangedLocked(id string) <-chan struct{} {
	if c.sessionChanges == nil {
		c.sessionChanges = make(map[string]chan struct{})
	}
	if c.sessionChanges[id] == nil {
		c.sessionChanges[id] = make(chan struct{})
	}
	return c.sessionChanges[id]
}

func (c *claudeProjects) findLiveSession(ctx context.Context, id string) (*provider.Adapter, error) {
	// Recovering one session must not wait indefinitely for unrelated roots.
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	adapters := c.adapters()
	claims := make(chan *provider.Adapter, len(adapters))
	failures := make(chan error, len(adapters))
	var wg sync.WaitGroup
	for _, a := range adapters {
		wg.Go(func() {
			rows, err := a.LiveSessions(probe)
			if err != nil {
				if !c.runtimeDefinitelyStopped(probe, a) {
					failures <- err
				}
				return
			}
			for _, row := range rows {
				if row.ID == id && platforms.ReloadSessionActive(row) {
					claims <- a
					return
				}
			}
		})
	}
	wg.Wait()
	close(claims)
	close(failures)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var discoveryErr error
	for err := range failures {
		discoveryErr = errors.Join(discoveryErr, err)
	}
	if err := errors.Join(discoveryErr, probe.Err()); err != nil {
		return nil, fmt.Errorf("%w: incomplete Claude supervisor discovery: %w", platforms.ErrPlatformUnreachable, err)
	}
	var owner *provider.Adapter
	for a := range claims {
		if owner != nil && owner != a {
			return nil, fmt.Errorf("%w: multiple Claude supervisors are executing session %s", platforms.ErrBusy, id)
		}
		owner = a
	}
	return owner, nil
}
