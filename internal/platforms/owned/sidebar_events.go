package owned

import (
	"context"
	"maps"

	"github.com/ocman/ocman/internal/sessioncatalog"
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
	previous := w.observations
	w.observations, w.available = maps.Clone(observations), available
	if notify == nil {
		return
	}
	notified := map[string]bool{}
	publish := func(row sessioncatalog.Session) {
		if !notified[row.ID] {
			notified[row.ID] = true
			notify(string(a.ID()), row.ID)
		}
	}
	for id := range changed {
		identity, err := a.canonical(ctx, id)
		if err != nil {
			continue
		}
		publish(identity)
		before, after := previous[id], observations[id]
		if !before.PendingPermission && !before.PendingQuestion && !after.PendingPermission && !after.PendingQuestion {
			continue
		}
		// Prompt queries follow native ancestry, never application parent links.
		// Resolve each edge through this adapter's owner/platform/source binding.
		seen := map[string]bool{identity.NativeID: true}
		for identity.NativeParentID != "" && !seen[identity.NativeParentID] {
			seen[identity.NativeParentID] = true
			identity, err = a.store.FindBinding(ctx, a.binding(identity.NativeParentID))
			if err != nil || identity.Purged || identity.CreationState != sessioncatalog.Ready {
				break
			}
			publish(identity)
		}
	}
}
