package platforms

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// fakePlatform is a minimal Platform used for registry tests. All
// capability-gated methods return ErrUnsupported so tests don't
// accidentally rely on them succeeding.
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

func (f *fakePlatform) Owns(_ context.Context, sessionID string) bool {
	for _, s := range f.sessions {
		if s.ID == sessionID {
			return true
		}
	}
	return false
}

func (f *fakePlatform) SessionsInactiveBefore(context.Context, int64) ([]db.SessionArchiveCandidate, error) {
	return nil, nil
}

func (f *fakePlatform) SessionChanges(context.Context, string) (*SessionChanges, error) {
	return nil, ErrUnsupported
}

func (f *fakePlatform) SessionInfo(context.Context, string) (*SessionInfo, error) {
	return nil, ErrUnsupported
}

func (f *fakePlatform) LiveStatus(string) *LiveState { return nil }

func (f *fakePlatform) AgentCatalog(context.Context, string) ([]AgentCatalogEntry, error) {
	return nil, nil
}
func (f *fakePlatform) SlashCommands(context.Context, string) ([]SlashCommandEntry, error) {
	return nil, nil
}
func (f *fakePlatform) SessionModels(context.Context, string) (*SessionModelsResponse, error) {
	return nil, ErrUnsupported
}
func (f *fakePlatform) ListPermissions(context.Context, string) ([]LivePrompt, error) {
	return nil, nil
}
func (f *fakePlatform) ListQuestions(context.Context, string) ([]LivePrompt, error) {
	return nil, nil
}
func (f *fakePlatform) SendMessage(context.Context, SendMessageRequest) error { return ErrUnsupported }
func (f *fakePlatform) ExecuteCommand(context.Context, ExecuteCommandRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) RunShell(context.Context, RunShellRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) RespondPermission(context.Context, RespondPermissionRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) RespondQuestion(context.Context, RespondQuestionRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) RejectQuestion(context.Context, RejectQuestionRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) Abort(context.Context, AbortRequest) error { return ErrUnsupported }
func (f *fakePlatform) RenameSession(context.Context, RenameSessionRequest) error {
	return ErrUnsupported
}
func (f *fakePlatform) Compact(context.Context, CompactRequest) error { return ErrUnsupported }
func (f *fakePlatform) CreateSession(context.Context, CreateSessionRequest) (*CreateSessionResponse, error) {
	return nil, ErrUnsupported
}
func (f *fakePlatform) ProxyEvents(context.Context, string, io.Writer, func()) error {
	return ErrUnsupported
}

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

// TestRegistry_PlatformForSession_OwnsFanout verifies that on a cold
// reverse-lookup cache (no RememberSessions call) the registry falls
// back to Platform.Owns rather than Platform.Session. This is the
// fast cold-path that avoids the heavy HTTP round-trip OpenCode's
// Session method makes.
func TestRegistry_PlatformForSession_OwnsFanout(t *testing.T) {
	reg := NewRegistry()
	a := &fakePlatform{
		id:        "a",
		available: true,
		sessions:  []db.Session{{ID: "owned-by-a"}},
	}
	b := &fakePlatform{
		id:        "b",
		available: true,
		sessions:  []db.Session{{ID: "owned-by-b"}},
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
	if !errors.Is(ErrUnsupported, ErrUnsupported) {
		t.Fatal("ErrUnsupported should match itself")
	}
}
