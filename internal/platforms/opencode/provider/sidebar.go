package opencode

import (
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func (a *Adapter) CachedSessionObservations() map[string]platforms.LiveState {
	out := map[string]platforms.LiveState{}
	if a.turns == nil {
		return out
	}
	a.turns.mu.RLock()
	for id, entry := range a.turns.entries {
		// Explicit lifecycle events are authoritative before bootstrap finishes.
		// Seeding is needed to infer idle from absence, not to trust an entry.
		if entry.port == "" {
			continue
		}
		status := db.SessionStatus("idle")
		if entry.busy {
			status = db.StatusBusy
		}
		out[id] = platforms.LiveState{Status: string(status)}
	}
	a.turns.mu.RUnlock()
	permissions, questions := a.prompts.pendingSessionIDs()
	for id := range permissions {
		v := out[id]
		v.PendingPermission = true
		out[id] = v
	}
	for id := range questions {
		v := out[id]
		v.PendingQuestion = true
		out[id] = v
	}
	return out
}
