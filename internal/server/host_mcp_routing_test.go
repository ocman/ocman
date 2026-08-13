package server

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
)

// mcpRoutingHost records the host-scoped calls the MCP adapters make, so
// a test can prove which machine they landed on.
type mcpRoutingHost struct {
	hostsvc.Host
	id string

	wtReq  *hostsvc.WorktreeSessionRequest
	gitDir string

	diffCalls int
	truncated bool
}

func (h *mcpRoutingHost) RemoteID() string { return h.id }

func (h *mcpRoutingHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.wtReq = &req
	return &hostsvc.WorktreeSessionResult{
		SessionID:    "ses_" + h.id,
		WorktreePath: req.ProjectDir + "/.worktrees/" + req.Branch,
		Branch:       req.Branch,
	}, nil
}

func (h *mcpRoutingHost) GitInfo(_ context.Context, dirs []string) (map[string]git.Info, error) {
	h.gitDir = strings.Join(dirs, ",")
	return map[string]git.Info{dirs[0]: {Branch: "branch-on-" + h.id}}, nil
}

func (h *mcpRoutingHost) GitDiff(_ context.Context, dir string, _ hostsvc.GitDiffOptions) (*git.Diff, error) {
	h.diffCalls++
	return &git.Diff{
		Repo:      dir,
		Truncated: h.truncated,
		Files: []git.DiffFile{
			{Path: "a.go", Additions: 3, Deletions: 1},
			{Path: "b.go", Additions: 1, Deletions: 0, Status: "untracked"},
		},
	}, nil
}

// routerWithRemote wires a Server whose router owns remoteDir on a remote
// host; every other dir belongs to the local host.
func routerWithRemote(t *testing.T, remoteDir string) (*Server, *mcpRoutingHost, *mcpRoutingHost) {
	t.Helper()
	local := &mcpRoutingHost{id: "local"}
	remote := &mcpRoutingHost{id: "r1"}
	router := hostsvc.NewRouter(local)
	router.RegisterRemote("r1", remote)
	router.SetDirResolver(func(dir string) string {
		if dir == remoteDir {
			return "r1"
		}
		return ""
	})
	srv := &Server{hostRouter: router}
	return srv, local, remote
}

// TestHostWorktreeSession_RoutesToOwner pins AD-16 for the MCP split
// path: the worktree (and its session) are created by the host that owns
// the project, never by the hub.
func TestHostWorktreeSession_RoutesToOwner(t *testing.T) {
	const remoteDir = "/remote/repo"
	srv, local, remote := routerWithRemote(t, remoteDir)

	res, err := srv.hostWorktreeSession(context.Background(), internalmcp.WorktreeSessionRequest{
		ParentDir: remoteDir,
		Branch:    "feat",
		NewBranch: true,
		BaseRef:   "main",
	})
	if err != nil {
		t.Fatalf("hostWorktreeSession: %v", err)
	}
	if local.wtReq != nil {
		t.Fatalf("worktree created on the hub: %+v", local.wtReq)
	}
	if remote.wtReq == nil {
		t.Fatal("worktree not created on the owning remote")
	}
	if remote.wtReq.ProjectDir != remoteDir || remote.wtReq.Branch != "feat" || !remote.wtReq.NewBranch || remote.wtReq.BaseRef != "main" {
		t.Errorf("owner received %+v; want the request verbatim", *remote.wtReq)
	}
	if res.SessionID != "ses_r1" || res.WorktreePath != remoteDir+"/.worktrees/feat" {
		t.Errorf("result = %+v; want the owner's session/worktree", res)
	}
}

// TestHostGitContext_RoutesToOwner pins that the prompt's git enrichment
// is read on the owning host, and rendered in `diff --stat` shape.
func TestHostGitContext_RoutesToOwner(t *testing.T) {
	const remoteDir = "/remote/repo"
	srv, local, remote := routerWithRemote(t, remoteDir)

	gc, err := srv.hostGitContext(context.Background(), remoteDir, true)
	if err != nil {
		t.Fatalf("hostGitContext: %v", err)
	}
	if local.gitDir != "" {
		t.Fatalf("git read on the hub for %q", local.gitDir)
	}
	if remote.gitDir != remoteDir {
		t.Errorf("owner read %q; want %q", remote.gitDir, remoteDir)
	}
	if gc.Branch != "branch-on-r1" {
		t.Errorf("branch = %q; want the owner's branch", gc.Branch)
	}
	for _, want := range []string{"a.go +3 -1", "b.go +1 -0 (untracked)", "2 file(s) changed, +4 -1"} {
		if !strings.Contains(gc.Changes, want) {
			t.Errorf("change summary %q missing %q", gc.Changes, want)
		}
	}
	if strings.Contains(gc.Changes, "truncated") {
		t.Errorf("change summary %q claims truncation for a complete diff", gc.Changes)
	}
}

// TestHostGitContext_BranchOnlyDoesNotFetchTheDiff pins that asking for
// the branch alone never pulls a full `git diff` (patch bodies plus
// untracked file contents, up to 2 MB, marshalled over gRPC for a remote
// owner) just to throw it away.
func TestHostGitContext_BranchOnlyDoesNotFetchTheDiff(t *testing.T) {
	const remoteDir = "/remote/repo"
	srv, _, remote := routerWithRemote(t, remoteDir)

	gc, err := srv.hostGitContext(context.Background(), remoteDir, false)
	if err != nil {
		t.Fatalf("hostGitContext: %v", err)
	}
	if remote.diffCalls != 0 {
		t.Errorf("fetched %d diff(s) for a branch-only request", remote.diffCalls)
	}
	if gc.Changes != "" {
		t.Errorf("changes = %q; want empty for a branch-only request", gc.Changes)
	}
	if gc.Branch != "branch-on-r1" {
		t.Errorf("branch = %q; want the owner's branch", gc.Branch)
	}
}

// TestHostGitContext_LabelsATruncatedDiff pins that a diff the host had
// to cut short is reported as such: the counts are a floor, not a fact,
// and an unlabelled undercount reads as authoritative to an agent.
func TestHostGitContext_LabelsATruncatedDiff(t *testing.T) {
	const remoteDir = "/remote/repo"
	srv, _, remote := routerWithRemote(t, remoteDir)
	remote.truncated = true

	gc, err := srv.hostGitContext(context.Background(), remoteDir, true)
	if err != nil {
		t.Fatalf("hostGitContext: %v", err)
	}
	if !strings.Contains(gc.Changes, "truncated") {
		t.Errorf("change summary %q does not mark the truncation", gc.Changes)
	}
}

// TestKillHostTmuxTarget_FailsClosedForRemoteOwner pins the deliberate
// fail-closed: hostsvc.Host has no kill-target method, so a remote-owned
// child must produce an error instead of killing a same-named pane here.
func TestKillHostTmuxTarget_FailsClosedForRemoteOwner(t *testing.T) {
	const remoteDir = "/remote/repo"
	srv, _, _ := routerWithRemote(t, remoteDir)

	err := srv.killHostTmuxTarget(context.Background(), remoteDir, "some-session:1")
	if err == nil {
		t.Fatal("expected a fail-closed error for a remote-owned session")
	}
	if !strings.Contains(err.Error(), "not supported for remote-owned sessions") {
		t.Errorf("error = %v; want a clear not-supported message", err)
	}
}

// TestGitContextIsSoftOnHostErrors proves a host that can't answer yields
// less context, not an error (the split must still proceed).
func TestGitContextIsSoftOnHostErrors(t *testing.T) {
	srv := &Server{hostRouter: hostsvc.NewRouter(&failingGitHost{})}
	gc, err := srv.hostGitContext(context.Background(), "/anywhere", true)
	if err != nil {
		t.Fatalf("hostGitContext must be soft, got %v", err)
	}
	if gc.Branch != "" || gc.Changes != "" {
		t.Errorf("expected empty context, got %+v", gc)
	}
}

type failingGitHost struct{ hostsvc.Host }

func (failingGitHost) RemoteID() string { return "local" }

func (failingGitHost) GitInfo(context.Context, []string) (map[string]git.Info, error) {
	return nil, errors.New("no git")
}

func (failingGitHost) GitDiff(context.Context, string, hostsvc.GitDiffOptions) (*git.Diff, error) {
	return nil, errors.New("no git")
}
