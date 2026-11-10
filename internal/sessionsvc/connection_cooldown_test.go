package sessionsvc

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/sessioncatalog"
)

func TestNamedCooldownDoesNotChangeSiblingModel(t *testing.T) {
	r := newConnectionRig(t)
	r.svc.hooks.ProjectModels = func(context.Context, string) ([]string, bool) { return []string{"a/1", "b/1"}, false }
	ids := make([]string, 2)
	for i, c := range r.connections {
		req := identityRequest()
		req.Creation.RequestKey = c.ID
		created, err := r.svc.Create(t.Context(), c.ID, req)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = created.ID
	}
	now := time.Unix(1000, 0)
	r.svc.cooldowns.now = func() time.Time { return now }
	r.svc.RecordSessionCooldown(t.Context(), ids[0], "a", time.Hour)
	for i, want := range []string{"b/1", "a/1"} {
		p, err := r.svc.resolve(t.Context(), ids[i], "")
		if err != nil {
			t.Fatal(err)
		}
		if got := r.svc.selectModel(t.Context(), p, ids[i], ""); got != want {
			t.Errorf("account %d model = %q, want %q", i, got, want)
		}
		if got, _, ok := r.svc.Fallthrough(t.Context(), "", ids[i]); !ok || got != want {
			t.Errorf("account %d fallthrough = %q %v, want %q", i, got, ok, want)
		}
	}
	if !r.svc.CooldownUntil("a").IsZero() || !r.svc.SessionCooldownUntil(t.Context(), ids[1], "a").IsZero() {
		t.Fatal("named cooldown leaked into global or sibling getter")
	}
	if got := r.svc.ConnectionCooldownUntil(r.connections[0].ID, "a"); !got.Equal(now.Add(time.Hour)) {
		t.Fatalf("connection cooldown = %v", got)
	}
	r.svc.RecordSessionCooldown(t.Context(), ids[0], "b", 2*time.Hour)
	if m, at, ok := r.svc.Fallthrough(t.Context(), "", ids[0]); !ok || m != "" || !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("exhausted account = %q %v %v", m, at, ok)
	}
	r.svc.RecordCooldown(t.Context(), "a", 3*time.Hour)
	if got, _, _ := r.svc.Fallthrough(t.Context(), "", ids[1]); got != "a/1" {
		t.Fatal("named model selection consulted a global cooldown")
	}
	now = now.Add(time.Hour)
	if !r.svc.SessionCooldownUntil(t.Context(), ids[0], "a").IsZero() {
		t.Fatal("expired account cooldown still active")
	}
}

func TestNamedRemoteModelUsesNativeNamespace(t *testing.T) {
	p := &namedRemoteIdentity{canonicalRemoteFake: &canonicalRemoteFake{fakePlatform: &fakePlatform{id: "r-remote:cnx_account"}}}
	s := New(&fakeRegistry{}, Hooks{})
	if got, err := s.sessionModel(t.Context(), p, "s", "opencode:a/1"); err != nil || got != "a/1" {
		t.Fatalf("native model = %q %v", got, err)
	}
	if _, err := s.sessionModel(t.Context(), p, "s", "claudecode:a/1"); err == nil {
		t.Fatal("foreign native namespace accepted")
	}
}

func TestSessionCooldownLegacyAndUnknownConnections(t *testing.T) {
	s := New(&fakeRegistry{}, Hooks{})
	s.RecordSessionCooldown(t.Context(), "legacy", "a", 0)
	if s.CooldownUntil("a").IsZero() || !s.SessionCooldownUntil(t.Context(), "other", "a").Equal(s.CooldownUntil("a")) ||
		!s.ConnectionCooldownUntil("", "a").Equal(s.CooldownUntil("a")) {
		t.Fatal("legacy provider-wide cooldown changed")
	}
	r := newConnectionRig(t)
	r.svc.RecordSessionCooldown(t.Context(), "missing", "a", time.Hour)
	if !r.svc.CooldownUntil("a").IsZero() || !r.svc.SessionCooldownUntil(t.Context(), "missing", "a").IsZero() ||
		!r.svc.ConnectionCooldownUntil("", "a").IsZero() {
		t.Fatal("unknown connection acquired cooldown authority")
	}
}

func TestNamedCooldownClampAndUnavailableAccount(t *testing.T) {
	r := newConnectionRig(t)
	created, err := r.svc.Create(t.Context(), r.connections[0].ID, identityRequest())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0)
	r.svc.cooldowns.now = func() time.Time { return now }
	r.svc.hooks.CooldownTimes = func(context.Context) (time.Duration, time.Duration) {
		return time.Minute, 10 * time.Minute
	}
	for _, tc := range []struct{ requested, want time.Duration }{
		{time.Second, time.Minute}, {0, 10 * time.Minute}, {-time.Second, 10 * time.Minute},
	} {
		r.svc.RecordSessionCooldown(t.Context(), created.ID, "a", tc.requested)
		if got := r.svc.SessionCooldownUntil(t.Context(), created.ID, "a"); !got.Equal(now.Add(tc.want)) {
			t.Fatalf("duration %v expires at %v, want %v", tc.requested, got, now.Add(tc.want))
		}
	}
	if err := r.store.SetEnabled(t.Context(), r.connections[0].ID, false); err != nil {
		t.Fatal(err)
	}
	r.svc.RecordSessionCooldown(t.Context(), created.ID, "b", time.Hour)
	if !r.svc.ConnectionCooldownUntil(r.connections[0].ID, "b").IsZero() || !r.svc.CooldownUntil("b").IsZero() {
		t.Fatal("unavailable account fell back to another cooldown scope")
	}
}

func TestNamedRemoteCooldownUsesExactSource(t *testing.T) {
	r := newConnectionRig(t)
	first := addNamedRemote(t, r, "ocm_"+uuid.NewString())
	first.info.SourceKey = "first"
	second := addNamedRemote(t, r, "ocm_"+uuid.NewString())
	second.info.SourceKey = "second"
	registry := r.svc.registry.(*platforms.Registry)
	registry.SetSourceResolver(func(_ context.Context, owner, kind, source string) (platforms.ID, error) {
		for _, p := range []*namedRemoteIdentity{first, second} {
			if owner == "remote" && kind == "opencode" && source == p.info.SourceKey {
				return p.ID(), nil
			}
		}
		return "", platforms.ErrNotFound
	})
	for _, p := range []*namedRemoteIdentity{first, second} {
		if err := r.catalogue.MirrorCanonical(t.Context(), sessioncatalog.Session{
			ID: p.info.ID, OwnerID: "remote", Platform: "opencode", SourceKey: p.info.SourceKey,
			Origin: sessioncatalog.User, CreationState: sessioncatalog.Ready, CreatedAt: 1, UpdatedAt: 2,
		}); err != nil {
			t.Fatal(err)
		}
		r.svc.sessionDirs.Store(sessionCacheKey{p.ID(), p.info.ID}, "/repo")
	}
	r.svc.hooks.ProjectModels = func(context.Context, string) ([]string, bool) { return []string{"a/1", "b/1"}, false }
	r.svc.RecordSessionCooldown(t.Context(), second.info.ID, "a", time.Hour)
	if r.svc.ConnectionCooldownUntil(string(second.ID()), "a").IsZero() ||
		!r.svc.SessionCooldownUntil(t.Context(), first.info.ID, "a").IsZero() || !r.svc.CooldownUntil("a").IsZero() {
		t.Fatal("remote cooldown did not retain exact owner/account identity")
	}
	for i, p := range []*namedRemoteIdentity{first, second} {
		want := []string{"a/1", "b/1"}[i]
		if got, _, ok := r.svc.Fallthrough(t.Context(), string(p.ID()), p.info.ID); !ok || got != want {
			t.Fatalf("remote account %d fallthrough = %q %v", i, got, ok)
		}
	}
	registry.SetSourceResolver(func(context.Context, string, string, string) (platforms.ID, error) { return "", platforms.ErrNotFound })
	r.svc.RecordSessionCooldown(t.Context(), first.info.ID, "b", time.Hour)
	if !r.svc.ConnectionCooldownUntil(string(first.ID()), "b").IsZero() {
		t.Fatal("unresolved remote source borrowed a same-kind connection")
	}
}
