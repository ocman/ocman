package owned

import (
	"context"

	"github.com/ocman/ocman/internal/statev2"
)

// Checkpoint first, notify second. Final task completion must refresh archives
// without inventing another foreground idle edge or draining the message queue.
func (w *sidebarWorker) publishBackgroundChanges(ctx context.Context, observations map[string]statev2.SidebarObservation) {
	a := w.adapter
	a.lifecycle.mu.Lock()
	notify := a.lifecycle.sink.OnSessionBackgroundChanged
	a.lifecycle.mu.Unlock()
	next := map[string][2]int{}
	for id, row := range observations {
		counts := [2]int{row.BackgroundTasks, row.BackgroundServices}
		next[id] = counts
		if notify == nil || counts == w.background[id] {
			continue
		}
		identity, err := a.canonical(ctx, id)
		if err == nil {
			notify(string(a.ID()), identity.ID)
		}
	}
	// A reconnect snapshot can omit a completed session entirely, rather than
	// publishing an explicit zero. Absence from the checkpoint must notify too.
	for id, counts := range w.background {
		if _, present := next[id]; present || counts == [2]int{} || notify == nil {
			continue
		}
		identity, err := a.canonical(ctx, id)
		if err == nil {
			notify(string(a.ID()), identity.ID)
		}
	}
	w.background = next
}
