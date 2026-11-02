package platforms_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/platformtest"
)

type fakePlatform = platformtest.Fake

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := platforms.NewRegistry()
	p := &fakePlatform{PlatformID: "opencode", Name: "OpenCode"}
	reg.Register(p)

	got, ok := reg.Get("opencode")
	if !ok {
		t.Fatalf("Get(\"opencode\") returned ok=false")
	}
	if got.ID() != "opencode" {
		t.Errorf("expected id=opencode, got %q", got.ID())
	}
}

func TestRegistry_GetUnknown(t *testing.T) {
	reg := platforms.NewRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Errorf("Get on unknown id should return ok=false")
	}
}

func TestRegistry_Platforms_StableOrder(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{PlatformID: "opencode"})
	reg.Register(&fakePlatform{PlatformID: "other-platform"})
	reg.Register(&fakePlatform{PlatformID: "codex"})

	got := reg.Platforms()
	if len(got) != 3 {
		t.Fatalf("expected 3 platforms, got %d", len(got))
	}
	// Registration order is preserved.
	want := []platforms.ID{"opencode", "other-platform", "codex"}
	for i, p := range got {
		if p.ID() != want[i] {
			t.Errorf("platform %d: expected %q, got %q", i, want[i], p.ID())
		}
	}
}

func TestRegistry_PlatformForSession_FromCache(t *testing.T) {
	reg := platforms.NewRegistry()
	p := &fakePlatform{
		PlatformID:  "opencode",
		SessionList: []db.Session{{ID: "s1", Platform: "opencode"}},
	}
	reg.Register(p)

	// Listing sessions should seed the reverse-lookup cache.
	ctx := context.Background()
	if _, err := p.Sessions(ctx, "", 0); err != nil {
		t.Fatalf("Sessions: %v", err)
	}
	reg.RememberSessions("opencode", []db.Session{{ID: "s1"}})

	got, ok := reg.PlatformForSession(ctx, "s1")
	if !ok {
		t.Fatalf("PlatformForSession(\"s1\") returned ok=false")
	}
	if got.ID() != "opencode" {
		t.Errorf("expected opencode, got %q", got.ID())
	}
}

func TestRegistry_PlatformForSession_Unknown(t *testing.T) {
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{PlatformID: "opencode"})

	if _, ok := reg.PlatformForSession(context.Background(), "unknown"); ok {
		t.Errorf("PlatformForSession on unknown id should return ok=false")
	}
}

// TestRegistry_PlatformForSession_OwnsFanout verifies that on a cold
// reverse-lookup cache (no RememberSessions call) the registry falls
// back to Platform.Owns rather than Platform.Session. This is the
// fast cold-path that avoids the heavy HTTP round-trip OpenCode's
// Session method makes.
func TestRegistry_PlatformForSession_OwnsFanout(t *testing.T) {
	reg := platforms.NewRegistry()
	a := &fakePlatform{
		PlatformID:  "a",
		SessionList: []db.Session{{ID: "owned-by-a"}},
	}
	b := &fakePlatform{
		PlatformID:  "b",
		SessionList: []db.Session{{ID: "owned-by-b"}},
	}
	reg.Register(a)
	reg.Register(b)

	got, ok := reg.PlatformForSession(context.Background(), "owned-by-b")
	if !ok {
		t.Fatalf("PlatformForSession(\"owned-by-b\") returned ok=false")
	}
	if got.ID() != "b" {
		t.Errorf("expected b, got %q", got.ID())
	}

	// Second call should hit the cache and still resolve correctly.
	got2, ok := reg.PlatformForSession(context.Background(), "owned-by-b")
	if !ok || got2.ID() != "b" {
		t.Errorf("cached lookup failed: ok=%v id=%q", ok, got2.ID())
	}
}

func TestErrUnsupported(t *testing.T) {
	if !errors.Is(platforms.ErrUnsupported, platforms.ErrUnsupported) {
		t.Fatal("ErrUnsupported should match itself")
	}
}
