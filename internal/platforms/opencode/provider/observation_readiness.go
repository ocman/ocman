package opencode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// SessionObservationsReady refuses to treat an unseeded cache as idle. Port
// discovery is read-only; existing headless watchers populate the caches.
func (a *Adapter) SessionObservationsReady(ctx context.Context) (bool, error) {
	ports := a.rt.DiscoverOpenCodePortsContext(ctx)
	if ports == nil {
		return false, platforms.ErrHistoryUnavailable
	}
	a.turns.mu.RLock()
	defer a.turns.mu.RUnlock()
	a.prompts.mu.RLock()
	defer a.prompts.mu.RUnlock()
	for directory, port := range ports {
		generation, seeded := a.turns.seeded[port]
		if !seeded || generation != a.turns.portGen[port] {
			return true, platforms.ErrHistoryUnavailable
		}
		// A reconciliation registers all candidate worktree scopes before it
		// starts. Require both the root scopes and every registered scope.
		for _, kind := range []string{"permission", "question"} {
			scope := promptScope(directory, kind)
			if a.prompts.scopePort[scope] != port || a.prompts.applied[scope] == 0 {
				return true, platforms.ErrHistoryUnavailable
			}
		}
		for scope, owner := range a.prompts.scopePort {
			if owner == port && a.prompts.applied[scope] == 0 {
				return true, platforms.ErrHistoryUnavailable
			}
		}
	}
	return len(ports) > 0, nil
}
