package owned

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/statev2"
)

// Publication follows persistence and the shared live snapshot. Subscribers can
// read the new compact row, rather than racing the half-second checkpoint loop.
func (w *sidebarWorker) publishSidebarChanges(ctx context.Context, observations map[string]statev2.SidebarObservation, available bool) {
	a := w.adapter
	a.lifecycle.mu.Lock()
	notify := a.lifecycle.sink.OnSidebarChanged
	a.lifecycle.mu.Unlock()
	changed := map[string]bool{}
	for id, row := range observations {
		previous, exists := w.observations[id]
		if !exists || row != previous || available != w.available {
			changed[id] = true
		}
	}
	for id := range w.observations {
		if _, exists := observations[id]; !exists {
			changed[id] = true
		}
	}
	w.observations, w.available = maps.Clone(observations), available
	if notify == nil {
		return
	}
	for id := range changed {
		identity, err := a.canonical(ctx, id)
		if err == nil {
			notify(string(a.ID()), identity.ID)
		}
	}
}
