package codextransport

import "github.com/ocman/ocman/internal/platforms"

var (
	_ platforms.CachedSessionObserver     = (*Platform)(nil)
	_ platforms.CachedSessionAvailability = (*Platform)(nil)
)

// Sidebar workers read only the current supervisor's event cache. They must
// never launch a runtime or infer another writer's liveness from history.
func (p *Platform) CachedSessionObservations() map[string]platforms.LiveState {
	if a := p.current(); a != nil {
		return a.CachedSessionObservations()
	}
	return nil
}

func (p *Platform) CachedSessionAvailable() bool {
	if a := p.current(); a != nil {
		return a.CachedSessionAvailable()
	}
	return false
}
