package provider

import (
	"testing"

	"github.com/ocman/ocman/internal/platforms"
)

func TestSidebarObservationsAreCacheOnlyAndDisconnectSafe(t *testing.T) {
	f := newFake(t, allCapabilities())
	a := f.adapter(t)
	a.live["root"] = platforms.LiveState{Status: "busy"}
	a.permissions["p"] = platforms.LivePrompt{"sessionID": "child"}
	a.questions["q"] = platforms.LivePrompt{"sessionID": "root"}
	if !a.CachedSessionAvailable() {
		t.Fatal("idle source cannot accept a new turn")
	}
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatal("unconnected stream published busy")
	}
	a.headlessConnected = true
	got := a.CachedSessionObservations()
	if got["root"].Status != "busy" || !got["root"].PendingQuestion || !got["child"].PendingPermission {
		t.Fatalf("observations=%+v", got)
	}
	delete(got, "root")
	if len(a.live) != 1 {
		t.Fatal("returned mutable live map")
	}
	a.headlessConnected = false
	if !a.CachedSessionAvailable() {
		t.Fatal("event reconnect disabled adoption of idle sessions")
	}
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatal("disconnected stream retained live observations")
	}
	a.headlessConnected = true
	a.draining = true
	if a.CachedSessionAvailable() {
		t.Fatal("draining source can accept a new turn")
	}
	if got := a.CachedSessionObservations(); len(got) != 0 {
		t.Fatal("draining source retained live observations")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.path != "/hello" {
			t.Fatalf("snapshot made RPC: %+v", call)
		}
	}
}
