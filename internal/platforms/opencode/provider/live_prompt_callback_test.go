package opencode

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestPromptSnapshotCallbacksRunOutsideRegistryLock(t *testing.T) {
	r := newLivePromptRegistry()
	called := false
	if !r.applySnapshot("/repo", "permission", 1, []platforms.LivePrompt{{"sessionID": "session", "id": "request"}}, func(platforms.LivePrompt) {
		called = true
		if !r.mu.TryRLock() {
			t.Error("snapshot callback holds prompt registry lock, canonical session lookup would deadlock")
			return
		}
		r.mu.RUnlock()
		permissions, _ := r.pendingSessionIDs()
		if !permissions["session"] {
			t.Error("callback ran before snapshot became visible")
		}
	}) || !called {
		t.Fatal("snapshot callback not delivered")
	}
}
