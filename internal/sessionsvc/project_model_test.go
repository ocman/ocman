package sessionsvc

import (
	"context"
	"testing"

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
	p := &dirPlatform{fakePlatform: &fakePlatform{id: "opencode", available: true}, dir: "/repo"}
	reg := &fakeRegistry{byID: map[platforms.ID]platforms.Platform{"opencode": p}, owner: p}
	return New(reg, Hooks{ProjectModels: func(_ context.Context, dir string) []string { return lists[dir] }}), p
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
