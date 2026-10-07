package server

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type mcpCreateHost struct {
	*ensureHost
	capable     bool
	base        string
	trees       []git.Worktree
	treesErr    error
	baseErr     error
	worktreeErr error
	request     hostsvc.WorktreeSessionRequest
}

func (h *mcpCreateHost) Capabilities() hostsvc.HostCaps {
	return hostsvc.HostCaps{OpencodeLaunch: h.capable}
}
func (h *mcpCreateHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	return h.trees, h.treesErr
}
func (h *mcpCreateHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) {
	return h.base, h.baseErr
}
func (h *mcpCreateHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.request = req
	return &hostsvc.WorktreeSessionResult{SessionID: "ses-new", WorktreePath: "/src/.worktrees/ocman/new"}, h.worktreeErr
}

func TestMCPCreateSession(t *testing.T) {
	srv := testServer(t)
	var ensured string
	host := &mcpCreateHost{ensureHost: &ensureHost{ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
		ensured = req.ProjectDir
		return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:6620", RepoRoot: req.ProjectDir}, nil
	}}}
	srv.hostRouter = hostsvc.NewRouter(host)
	var created platforms.CreateSessionRequest
	var sent platforms.SendMessageRequest
	sendErr := error(nil)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "opencode",
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			if id != "ses-caller" {
				return nil, platforms.ErrNotFound
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Directory: "/src/.worktrees/ocman/feat-x"}}, nil
		},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			created = req
			return &platforms.CreateSessionResponse{ID: "ses-new"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error { sent = req; return sendErr },
	})
	srv.registry = reg
	svc := sessionMCPService{srv}
	if err := srv.stateDB.SetProjectSettings(t.Context(), "/src/ocman", state.ProjectSettings{Models: []string{"p/default"}}); err != nil {
		t.Fatal(err)
	}
	if err := srv.stateDB.SetSetting(t.Context(), defaultAgentKey, "custom"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "defaults", Directory: "/src/ocman"}); err != nil {
		t.Fatal(err)
	}
	if sent.Model != "p/default" || sent.Agent != "custom" {
		t.Fatalf("defaults = %#v", sent)
	}
	host.capable, host.base = true, "main"
	gotDefault, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "defaults", Directory: "/src/ocman"})
	if err != nil || gotDefault.Directory != "/src/.worktrees/ocman/new" || !host.request.AutoName || host.request.Prompt != "defaults" || sent.Model != "p/default" {
		t.Fatalf("worktree default = %#v, %v, request %#v, sent %#v", gotDefault, err, host.request, sent)
	}
	useWorktree := false
	gotOverride, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "override", Directory: "/src/ocman", Worktree: &useWorktree, Model: "p/override", Agent: "plan"})
	if err != nil || gotOverride.Directory != "/src/ocman" || sent.Model != "p/override" || sent.Agent != "plan" {
		t.Fatalf("override = %#v, %v, sent %#v", gotOverride, err, sent)
	}
	useWorktree = true
	host.worktreeErr = errors.New("worktree failed")
	if _, err := svc.CreateSession(t.Context(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/src/ocman", Worktree: &useWorktree}); !errors.Is(err, host.worktreeErr) {
		t.Fatalf("worktree error = %v", err)
	}
	host.capable = false

	// No directory: the caller's worktree folds to its project root.
	got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Model: "p/m", Agent: "plan", Platform: "opencode", SessionID: "ses-caller"})
	if err != nil || got != (internalmcp.CreatedSession{Platform: "opencode", SessionID: "ses-new", Directory: "/src/ocman"}) {
		t.Fatalf("CreateSession = %#v, %v", got, err)
	}
	if ensured != "/src/ocman" || created.Directory != "/src/ocman" || created.Port != "6620" {
		t.Fatalf("ensured = %q, created = %#v", ensured, created)
	}
	if sent.SessionID != "ses-new" || sent.Message != "go" || sent.Model != "p/m" || sent.Agent != "plan" {
		t.Fatalf("sent = %#v", sent)
	}

	// Explicit directory, no caller: local opencode, directory kept as given.
	if got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/src/.worktrees/ocman/feat-y"}); err != nil || got.Directory != "/src/.worktrees/ocman/feat-y" || ensured != "/src/ocman" {
		t.Fatalf("explicit directory = %#v, %v, ensured %q", got, err, ensured)
	}

	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Platform: "opencode", SessionID: "missing"}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing caller err = %v", err)
	}
	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo", Platform: "nope", SessionID: "x"}); !errors.Is(err, internalmcp.ErrInvalidSessionCreate) {
		t.Fatalf("unknown platform err = %v", err)
	}
	if _, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo", Platform: "r-gone:opencode", SessionID: "x"}); !errors.Is(err, internalmcp.ErrInvalidSessionCreate) {
		t.Fatalf("disconnected remote err = %v", err)
	}

	sendErr = errors.New("send failed")
	if got, err := svc.CreateSession(context.Background(), internalmcp.CreateSessionRequest{Prompt: "go", Directory: "/repo"}); err == nil || got.SessionID != "ses-new" {
		t.Fatalf("send failure = %#v, %v", got, err)
	}
}

func TestMCPCreateSessionRemote(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	owner := &autoWorktreeOwner{}
	srv.hostRouter = hostsvc.NewRouter(nil)
	srv.hostRouter.RegisterRemote("machine", owner)
	var sent platforms.SendMessageRequest
	reg.Register(&fakePlatform{id: "r-machine:opencode", sendMessageFn: func(req platforms.SendMessageRequest) error {
		sent = req
		return nil
	}})
	if err := srv.stateDB.SetProjectSettings(t.Context(), "/remote/repo", state.ProjectSettings{Models: []string{"p/project"}}); err != nil {
		t.Fatal(err)
	}
	useWorktree := true
	got, err := (sessionMCPService{srv}).CreateSession(t.Context(), internalmcp.CreateSessionRequest{
		Prompt: "fix it", Title: "Fix", Directory: "/remote/repo", Platform: "r-machine:opencode", Worktree: &useWorktree,
	})
	if err != nil || got.Platform != "r-machine:opencode" || got.Directory != "/remote/worktree" || got.SessionID != "child" {
		t.Fatalf("remote create = %#v, %v", got, err)
	}
	if owner.request.ProjectDir != "/remote/repo" || owner.request.Title != "Fix" || sent.Model != "p/project" || sent.Agent != "build" || sent.SessionID != "child" {
		t.Fatalf("remote request = %#v, sent %#v", owner.request, sent)
	}
}

func TestMCPWorktreeEligibility(t *testing.T) {
	for _, test := range []struct {
		name string
		host mcpCreateHost
		want bool
	}{
		{name: "unsupported"},
		{name: "non repo", host: mcpCreateHost{capable: true, treesErr: git.ErrNotARepo}},
		{name: "no base", host: mcpCreateHost{capable: true}},
		{name: "eligible", host: mcpCreateHost{capable: true, base: "main"}, want: true},
		{name: "linked", host: mcpCreateHost{capable: true, base: "main", trees: []git.Worktree{{Path: "/repo"}}}},
		{name: "main", host: mcpCreateHost{capable: true, base: "main", trees: []git.Worktree{{Path: "/repo", Main: true}}}, want: true},
		{name: "list error", host: mcpCreateHost{capable: true, treesErr: errors.New("list failed")}},
		{name: "base error", host: mcpCreateHost{capable: true, baseErr: errors.New("base failed")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := mcpWorktreeEligible(t.Context(), &test.host, "/repo/sub")
			wantErr := test.host.treesErr
			if errors.Is(wantErr, git.ErrNotARepo) {
				wantErr = nil
			}
			if test.host.baseErr != nil {
				wantErr = test.host.baseErr
			}
			if got != test.want || !errors.Is(err, wantErr) {
				t.Fatalf("eligible = %v, %v", got, err)
			}
		})
	}
}
