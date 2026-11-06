package owned

import (
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/api"
	opencode "github.com/ocman/ocman/internal/platforms/opencode/provider"
)

type statusCacheNative struct {
	*nativePlatform
	cache *opencode.Adapter
}

func (p *statusCacheNative) OpenCodeObserver() platforms.OpenCodeEventObserver { return p.cache }
func (p *statusCacheNative) SettleStoredSessionStatus(id, port string, inferred db.SessionStatus) db.SessionStatus {
	return p.cache.SettleStoredSessionStatus(id, port, inferred)
}

func TestCanonicalEventStatusUsesActualPortAndGenerationCache(t *testing.T) {
	a, p, id := storedFixture(t)
	cache := opencode.New(nil, nil, nil, ocapi.Auth{}) // No native history DB exists.
	a.native = &statusCacheNative{nativePlatform: p, cache: cache}
	observer := platforms.OpenCodeObserver(a)
	assertStatus := func(port string, want db.SessionStatus) {
		t.Helper()
		got, err := observer.SessionStatusOnPort(id, port)
		if err != nil || got != want {
			t.Fatalf("port %q: %s %v, want %s", port, got, err, want)
		}
	}
	generation := observer.StatusPortGeneration("7777")
	if !observer.ObserveSessionStatus("7777", generation, id, "busy") {
		t.Fatal("current event rejected")
	}
	assertStatus("7777", db.StatusBusy)
	assertStatus("8888", db.StatusWaiting)
	assertStatus("", db.StatusWaiting)
	observer.ClearSessionStatusForPort("7777")
	if observer.ObserveSessionStatus("7777", generation, id, "busy") {
		t.Fatal("stale generation accepted")
	}
	assertStatus("7777", db.StatusWaiting)
	generation = observer.StatusPortGeneration("7777")
	if !observer.ObserveSessionStatus("7777", generation, id, "idle") {
		t.Fatal("idle rejected")
	}
	assertStatus("7777", db.StatusWaiting)
	if !observer.ObserveSessionStatus("7777", generation, id, "busy") {
		t.Fatal("busy rejected")
	}
	p.source = "replacement"
	assertStatus("7777", db.StatusWaiting)
	if observer.ObserveSessionStatus("7777", generation, id, "busy") {
		t.Fatal("replacement source borrowed native ID")
	}
}
