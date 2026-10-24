package claudecode

import (
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (p *claudePlatform) CachedSessionObservations() map[string]platforms.LiveState {
	out := p.Adapter.CachedSessionObservations()
	unboundChildren, ambiguous := map[string]bool{}, map[string]bool{}
	for _, adapter := range p.projects.adapters() {
		for id, live := range adapter.CachedSessionObservations() {
			owner := p.projects.owner(id)
			if owner == adapter {
				out[id] = live
			} else if owner == nil && strings.HasPrefix(id, "agent-") {
				// A reattached supervisor can replay a child's terminal status
				// without ever observing it busy. These are execution events,
				// not shared transcript listings. Preserve an unambiguous claim.
				if unboundChildren[id] {
					ambiguous[id] = true
				}
				unboundChildren[id] = true
				out[id] = live
			}
		}
	}
	for id := range ambiguous {
		delete(out, id)
	}
	return out
}
