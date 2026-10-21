package claudecode

import "github.com/NoUseFreak/ocman/internal/platforms"

func (p *claudePlatform) CachedSessionObservations() map[string]platforms.LiveState {
	out := p.Adapter.CachedSessionObservations()
	for _, adapter := range p.projects.adapters() {
		for id, live := range adapter.CachedSessionObservations() {
			if owner := p.projects.owner(id); owner == adapter {
				out[id] = live
			}
		}
	}
	return out
}
