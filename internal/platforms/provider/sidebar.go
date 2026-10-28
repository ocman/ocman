package provider

import (
	"maps"

	"github.com/ocman/ocman/internal/platforms"
)

func (a *Adapter) CachedSessionAvailable() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return !a.draining && (a.ctx == nil || a.ctx.Err() == nil)
}

func (a *Adapter) CachedSessionObservations() map[string]platforms.LiveState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := map[string]platforms.LiveState{}
	if !a.headlessConnected || a.draining || (a.ctx != nil && a.ctx.Err() != nil) {
		return out
	}
	maps.Copy(out, a.live)
	for _, p := range a.permissions {
		if id, ok := p["sessionID"].(string); ok {
			v := out[id]
			v.PendingPermission = true
			out[id] = v
		}
	}
	for _, p := range a.questions {
		if id, ok := p["sessionID"].(string); ok {
			v := out[id]
			v.PendingQuestion = true
			out[id] = v
		}
	}
	return out
}
