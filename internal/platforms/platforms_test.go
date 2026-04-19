package platforms

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// fakePlatform is a minimal Platform used for registry tests.
type fakePlatform struct {
	id          ID
	displayName string
	available   bool
	caps        Capabilities
	sessions    []db.Session
}

func (f *fakePlatform) ID() ID                         { return f.id }
func (f *fakePlatform) DisplayName() string            { return f.displayName }
func (f *fakePlatform) Available(context.Context) bool { return f.available }
func (f *fakePlatform) Capabilities() Capabilities     { return f.caps }

func (f *fakePlatform) Sessions(context.Context, string, int64) ([]db.Session, error) {
	return f.sessions, nil
}

func (f *fakePlatform) Session(context.Context, string, int, int) (*SessionDetail, error) {
	return nil, ErrUnsupported
}

func (f *fakePlatform) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return nil, nil
}

func (f *fakePlatform) LiveStatus(string) *LiveState { return nil }

func TestRegistry_RegisterAndGet(t *testing.T) {
	reg := NewRegistry()
	p := &fakePlatform{id: "opencode", displayName: "OpenCode", available: true}
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
	reg := NewRegistry()
	if _, ok := reg.Get("nope"); ok {
		t.Errorf("Get on unknown id should return ok=false")
	}
}

func TestRegistry_Platforms_StableOrder(t *testing.T) {
	reg := NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})
	reg.Register(&fakePlatform{id: "claude-code"})
	reg.Register(&fakePlatform{id: "codex"})

	got := reg.Platforms()
	if len(got) != 3 {
		t.Fatalf("expected 3 platforms, got %d", len(got))
	}
	// Registration order is preserved.
	want := []ID{"opencode", "claude-code", "codex"}
	for i, p := range got {
		if p.ID() != want[i] {
			t.Errorf("platform %d: expected %q, got %q", i, want[i], p.ID())
		}
	}
}

func TestRegistry_PlatformForSession_FromCache(t *testing.T) {
	reg := NewRegistry()
	p := &fakePlatform{
		id:        "opencode",
		available: true,
		sessions:  []db.Session{{ID: "s1", Platform: "opencode"}},
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
	reg := NewRegistry()
	reg.Register(&fakePlatform{id: "opencode"})

	if _, ok := reg.PlatformForSession(context.Background(), "unknown"); ok {
		t.Errorf("PlatformForSession on unknown id should return ok=false")
	}
}

func TestErrUnsupported(t *testing.T) {
	if !errors.Is(ErrUnsupported, ErrUnsupported) {
		t.Fatal("ErrUnsupported should match itself")
	}
}
