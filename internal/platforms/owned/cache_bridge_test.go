package owned

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

type nativeCachePlatform struct {
	*nativePlatform
	seen string
}

func (p *nativeCachePlatform) MarkSessionDirty(id string) { p.seen = id }
func (p *nativeCachePlatform) RefreshSession(_ context.Context, id string) error {
	p.seen = id
	return nil
}
func (p *nativeCachePlatform) InstancePorts() platforms.InstancePorts { return testInstancePorts{} }

type testInstancePorts struct{}

func (testInstancePorts) DiscoverOpenCodePorts() map[string]string {
	return map[string]string{"/repo": "4100"}
}
func (testInstancePorts) DiscoverOpenCodePort(string) string { return "4100" }

// Invalidation and refresh go to ingestion, never to a native browsing cache.
func TestCanonicalCacheAndInstanceDiscoveryBridges(t *testing.T) {
	a, p, _ := fixture(t)
	publish(t, a, p.rows...)
	rows, err := a.Sessions(t.Context(), "", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := rows[0].ID
	native := &nativeCachePlatform{nativePlatform: p}
	a.native = native
	if ports := a.InstancePorts(); ports == nil || ports.DiscoverOpenCodePort("/repo") != "4100" || ports.DiscoverOpenCodePorts()["/repo"] != "4100" {
		t.Fatal("canonical wrapper lost instance discovery")
	}
	var notified, refreshed string
	a.MarkSessionDirty(id) // no ingestion bound yet: a no-op
	a.BindStoredHistory(platforms.StoredHistoryHooks{
		Notify:  func(id string) { notified = id },
		Refresh: func(_ context.Context, id string) error { refreshed = id; return nil },
	})
	a.MarkSessionDirty(id)
	if err := a.RefreshSession(t.Context(), id); err != nil || notified != id || refreshed != id {
		t.Fatalf("ingestion bridge: notify=%q refresh=%q %v", notified, refreshed, err)
	}
	if native.seen != "" {
		t.Fatalf("native browsing cache touched with %q", native.seen)
	}
	if err := a.RefreshSession(t.Context(), "native"); err == nil || refreshed != id {
		t.Fatal("raw refresh accepted")
	}
	a.native = p
	if a.InstancePorts() != nil {
		t.Fatal("invented instance discovery")
	}
	a.BindStoredHistory(platforms.StoredHistoryHooks{})
	if err := a.RefreshSession(t.Context(), id); !errors.Is(err, platforms.ErrHistoryUnavailable) {
		t.Fatalf("refresh without ingestion: %v", err)
	}
	if platforms.OpenCodeObserver(a) != nil {
		t.Fatal("invented event observer")
	}
}
