package sessionsvc

import (
	"context"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// dirPlatform serves a session detail rooted at dir and counts lookups.
type dirPlatform struct {
	*fakePlatform
	dir     string
	lookups int
}

func (p *dirPlatform) Session(_ context.Context, id string, _, _ int) (*platforms.SessionDetail, error) {
	p.lookups++
	return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: p.dir}}, nil
}

func newProjectModelService(lists map[string][]string) (*Service, *dirPlatform) {
	return newFallthroughService(lists, false)
}

func newFallthroughService(lists map[string][]string, off bool) (*Service, *dirPlatform) {
	p := &dirPlatform{fakePlatform: &fakePlatform{id: "opencode", available: true}, dir: "/repo"}
	reg := &fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": p}, owner: p}
	return New(reg, Hooks{ProjectModels: func(_ context.Context, dir string) ([]string, bool) { return lists[dir], off }}), p
}

func TestSendMessageProjectDefault(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, model, want string
		lists             map[string][]string
	}{
		{"empty model uses first configured", "", "prov/a", map[string][]string{"/repo": {"prov/a", "prov/b"}}},
		{"named model untouched", "prov/x", "prov/x", map[string][]string{"/repo": {"prov/a"}}},
		{"no list leaves model empty", "", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, p := newProjectModelService(tc.lists)
			if err := svc.SendMessage(ctx, "opencode", platforms.SendMessageRequest{SessionID: "s1", Message: "hi", Model: tc.model}); err != nil {
				t.Fatal(err)
			}
			if got := p.sent[0].Model; got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestExecuteCommandProjectDefault(t *testing.T) {
	ctx := context.Background()
	svc, p := newProjectModelService(map[string][]string{"/repo": {"prov/a"}})
	for _, model := range []string{"", "prov/x"} {
		if err := svc.ExecuteCommand(ctx, "opencode", platforms.ExecuteCommandRequest{SessionID: "s1", Command: "compact", Model: model}); err != nil {
			t.Fatal(err)
		}
	}
	if p.commands[0].Model != "prov/a" || p.commands[1].Model != "prov/x" {
		t.Fatalf("commands = %+v", p.commands)
	}
}

func TestProjectDefaultCachesDirectoryUntilMove(t *testing.T) {
	ctx := context.Background()
	svc, p := newProjectModelService(map[string][]string{"/repo": {"prov/a"}, "/other": {"prov/o"}})
	send := func() string {
		t.Helper()
		if err := svc.SendMessage(ctx, "opencode", platforms.SendMessageRequest{SessionID: "s1", Message: "hi"}); err != nil {
			t.Fatal(err)
		}
		return p.sent[len(p.sent)-1].Model
	}
	send()
	send()
	if p.lookups != 1 {
		t.Fatalf("lookups = %d, want 1 (directory cached)", p.lookups)
	}
	p.dir = "/other"
	if err := svc.Move(ctx, "opencode", platforms.MoveSessionRequest{SessionID: "s1", Directory: "/other"}); err != nil {
		t.Fatal(err)
	}
	if got := send(); got != "prov/o" || p.lookups != 2 {
		t.Fatalf("after move model = %q lookups = %d", got, p.lookups)
	}
}

func TestSendMessageSkipsCooledProvider(t *testing.T) {
	ctx := context.Background()
	list := map[string][]string{"/repo": {"a/1", "a/2", "b/1", "c/1"}}
	for _, tc := range []struct {
		name, model, want string
		cooled            []string
		off               bool
	}{
		{"nothing cooled keeps pick", "a/2", "a/2", nil, false},
		{"cooled default walks to next provider", "", "b/1", []string{"a"}, false},
		{"explicit pick overridden", "a/2", "b/1", []string{"a"}, false},
		{"walk skips every cooled provider", "a/1", "c/1", []string{"a", "b"}, false},
		{"pick outside list replaced", "x/9", "a/1", []string{"x"}, false},
		{"uncooled pick outside list kept", "x/9", "x/9", []string{"a"}, false},
		{"nothing usable leaves request untouched", "b/1", "b/1", []string{"a", "b", "c"}, false},
		{"off disables substitution", "a/1", "a/1", []string{"a"}, true},
		{"off still fills the default", "", "a/1", []string{"a"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, p := newFallthroughService(list, tc.off)
			for _, prov := range tc.cooled {
				svc.RecordCooldown(ctx, prov, time.Hour)
			}
			if err := svc.SendMessage(ctx, "opencode", platforms.SendMessageRequest{SessionID: "s1", Message: "hi", Model: tc.model}); err != nil {
				t.Fatal(err)
			}
			if got := p.sent[0].Model; got != tc.want {
				t.Fatalf("model = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRecordCooldownClampAndExpiry(t *testing.T) {
	ctx := context.Background()
	patience, fallback := 5*time.Minute, 15*time.Minute
	for _, tc := range []struct {
		name      string
		requested time.Duration
		lasts     time.Duration
	}{
		{"short request floored at patience", time.Second, patience},
		{"zero uses fallback", 0, fallback},
		{"negative uses fallback", -time.Minute, fallback},
		{"long request kept", time.Hour, time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(&fakeRegistry{}, Hooks{CooldownTimes: func(context.Context) (time.Duration, time.Duration) { return patience, fallback }})
			now := time.Unix(1_000, 0)
			svc.cooldowns.now = func() time.Time { return now }
			svc.RecordCooldown(ctx, "a", tc.requested)
			now = now.Add(tc.lasts - time.Nanosecond)
			if !svc.cooldowns.cooled("a") {
				t.Fatal("cooldown ended early")
			}
			now = now.Add(time.Nanosecond)
			if svc.cooldowns.cooled("a") {
				t.Fatal("expired cooldown still applies")
			}
		})
	}
}

func TestRecordCooldownDefaultsWithoutHook(t *testing.T) {
	svc := New(&fakeRegistry{}, Hooks{})
	now := time.Unix(1_000, 0)
	svc.cooldowns.now = func() time.Time { return now }
	svc.RecordCooldown(context.Background(), "a", 0)
	now = now.Add(DefaultCooldownFallback - time.Second)
	if !svc.cooldowns.cooled("a") {
		t.Fatal("default fallback not applied")
	}
}

func TestFallthrough(t *testing.T) {
	ctx := context.Background()
	list := map[string][]string{"/repo": {"a/1", "b/1", "c/1"}}
	svc, _ := newFallthroughService(list, false)
	now := time.Unix(1_000, 0)
	svc.cooldowns.now = func() time.Time { return now }
	if m, _, ok := svc.Fallthrough(ctx, "opencode", "s1"); !ok || m != "a/1" {
		t.Fatalf("fresh = %q %v", m, ok)
	}
	svc.RecordCooldown(ctx, "a", 2*time.Hour)
	svc.RecordCooldown(ctx, "c", time.Hour)
	if m, _, _ := svc.Fallthrough(ctx, "opencode", "s1"); m != "b/1" {
		t.Fatalf("after a, c = %q", m)
	}
	svc.RecordCooldown(ctx, "b", 3*time.Hour)
	if m, at, ok := svc.Fallthrough(ctx, "opencode", "s1"); !ok || m != "" || !at.Equal(now.Add(time.Hour)) {
		t.Fatalf("exhausted = %q %v %v", m, at, ok)
	}
	for name, svc := range map[string]*Service{
		"off":     func() *Service { s, _ := newFallthroughService(list, true); return s }(),
		"no list": func() *Service { s, _ := newFallthroughService(nil, false); return s }(),
	} {
		if _, _, ok := svc.Fallthrough(ctx, "opencode", "s1"); ok {
			t.Errorf("%s: fallthrough reported", name)
		}
	}
}
