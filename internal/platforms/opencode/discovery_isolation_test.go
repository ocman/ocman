package opencode

import (
	"context"
	"testing"
)

// A backend reload builds a new adapter; nothing it routes on may be
// inherited from the previous one, and two adapters never share state.
func TestAdaptersOwnTheirDiscovery(t *testing.T) {
	prev := newAdapterDiscovery
	newAdapterDiscovery = NewDiscovery
	t.Cleanup(func() { newAdapterDiscovery = prev })

	old, fresh := New(nil, nil), New(nil, nil)
	old.Discovery().SetMachineServer("4100")
	old.Discovery().SetManagedPortSource(func(context.Context) map[string]string { return map[string]string{"/repo": "4200"} })
	old.Discovery().rememberSessionPort("ses_1", "4200")
	if got := old.Discovery().DiscoverOpenCodePorts()["/repo"]; got != "4200" {
		t.Fatalf("old adapter ports = %q", got)
	}

	if got := fresh.Discovery().MachineServerPort(); got != "" {
		t.Fatalf("fresh machine server = %q, want empty", got)
	}
	if got := fresh.Discovery().DiscoverOpenCodePorts(); len(got) != 0 {
		t.Fatalf("fresh ports = %v, want none", got)
	}
	if got := fresh.Discovery().preferredSessionPort("ses_1"); got != "" {
		t.Fatalf("fresh affinity = %q, want empty", got)
	}
	var none *Discovery
	if none.MachineServerPort() != "" || none.DiscoverOpenCodePortsContext(t.Context()) != nil || none.DiscoverOpenCodePortContext(t.Context(), "/repo") != "" {
		t.Fatal("nil discovery must report no OpenCode")
	}
}

// State that stays package-level is cleared by ResetCaches, which the
// backend reload runs before the new registry is built.
func TestResetCachesClearsPackageHTTPCaches(t *testing.T) {
	catalogCache.put("4300", "/agent", []byte(`[]`))
	sessionCache.put("4300", "/session/x", []byte(`{}`))
	gen := catalogCache.generation("4300")

	ResetCaches()

	if _, ok := catalogCache.get("4300", "/agent"); ok {
		t.Fatal("catalog cache survived reset")
	}
	if _, ok := sessionCache.get("4300", "/session/x"); ok {
		t.Fatal("session cache survived reset")
	}
	// A fetch that started before the reset must not repopulate it.
	catalogCache.putForGeneration("4300", "/agent", []byte(`[]`), gen)
	if _, ok := catalogCache.get("4300", "/agent"); ok {
		t.Fatal("in-flight fetch from before reset was cached")
	}
}
