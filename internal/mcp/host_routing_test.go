package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/state"

	"github.com/mark3labs/mcp-go/mcptest"
)

// remoteOwnedDir is a directory that does not exist on this machine: it
// stands in for a project owned by a remote host. Any code path that
// shells out to local git for it fails, which is exactly what these
// tests pin — MCP must delegate to the owner-resolved host instead.
const remoteOwnedDir = "/remote-owned/does/not/exist/repo"

// buildMCPServerWithDeps registers the MCP tools for caller-supplied
// deps so a test can inject exactly the host seam it wants to observe.
func buildMCPServerWithDeps(t *testing.T, deps internalmcp.Deps) *mcptest.Server {
	t.Helper()
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(deps)...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// TestNewSession_WorktreeRoutesToOwningHost pins AD-16 for the MCP split
// path: creating a worktree is a host operation, so it must be delegated
// to the owner-resolved host, never resolved with a direct local git
// call. The parent session lives in a directory this machine does not
// have, so local git resolution can only fail — the call can succeed
// only by going through the injected host seam.
func TestNewSession_WorktreeRoutesToOwningHost(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-remote", Title: "parent", Directory: remoteOwnedDir, TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "unexpected-local-create"}

	// The owning host creates both the worktree and the session on it.
	var gotReq internalmcp.WorktreeSessionRequest
	creator := internalmcp.WorktreeSessionCreator(func(_ context.Context, req internalmcp.WorktreeSessionRequest) (*internalmcp.WorktreeSessionResult, error) {
		gotReq = req
		return &internalmcp.WorktreeSessionResult{
			SessionID:    "child-on-owner",
			WorktreePath: remoteOwnedDir + "/.worktrees/feat",
			Branch:       req.Branch,
		}, nil
	})
	// Any git/instance work on this machine is a routing bug.
	ensureCalls := 0

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		OcDB:                  ocDB,
		StateDB:               stateDB,
		Platform:              platform,
		PlatformID:            "opencode",
		CreateWorktreeSession: creator,
		EnsureProjectOpencode: internalmcp.ProjectOpencodeEnsurer(func(_ context.Context, _ string) (string, error) {
			ensureCalls++
			return "4242", nil
		}),
	})

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-remote",
		"intent":     "work on the remote project",
		"worktree":   true,
		"branch":     "feat",
	})
	if result.IsError {
		t.Fatalf("worktree split for a host-owned project failed: %s", resultText(result))
	}
	if gotReq.ParentDir != remoteOwnedDir || gotReq.Branch != "feat" || !gotReq.NewBranch {
		t.Fatalf("worktree creation did not reach the host seam: %+v", gotReq)
	}
	// The child is the session the owner created — nothing was created here.
	if platform.createCalls != 0 {
		t.Errorf("CreateSession ran on this machine %d times", platform.createCalls)
	}
	if ensureCalls != 0 {
		t.Errorf("ensureProjectOpencode ran on this machine %d times", ensureCalls)
	}
	cs, err := stateDB.GetChildSession("child-on-owner")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.Branch != "feat" || cs.WorktreePath != remoteOwnedDir+"/.worktrees/feat" {
		t.Fatalf("child record does not describe the owner's worktree: %+v", cs)
	}
}

// TestNewSession_WorktreeFailsClosedWithoutHostSeam proves the tool
// refuses instead of quietly creating a worktree on this machine when no
// owner-routed host adapter is wired.
func TestNewSession_WorktreeFailsClosedWithoutHostSeam(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-remote2", Title: "parent", Directory: remoteOwnedDir, TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "unexpected-local-create"}

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		OcDB:       ocDB,
		StateDB:    stateDB,
		Platform:   platform,
		PlatformID: "opencode",
	})

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-remote2",
		"intent":     "work on the remote project",
		"worktree":   true,
		"branch":     "feat",
	})
	if !result.IsError {
		t.Fatalf("expected a fail-closed error, got: %s", resultText(result))
	}
	if platform.createCalls != 0 {
		t.Errorf("CreateSession ran on this machine %d times", platform.createCalls)
	}
}

// TestNewSession_GitContextComesFromTheOwningHost pins that the prompt's
// branch / uncommitted-changes context is read on the host that owns the
// session's directory.
func TestNewSession_GitContextComesFromTheOwningHost(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-ctx", Title: "parent", Directory: remoteOwnedDir, TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-ctx"}

	var gotDirs []string
	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		OcDB:       ocDB,
		StateDB:    stateDB,
		Platform:   platform,
		PlatformID: "opencode",
		GitContext: internalmcp.GitContextReader(func(_ context.Context, dir string, _ bool) (internalmcp.GitContext, error) {
			gotDirs = append(gotDirs, dir)
			return internalmcp.GitContext{Branch: "owner-branch", Changes: "owner.go +1 -0"}, nil
		}),
	})

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-ctx",
		"intent":     "look around",
	})
	if result.IsError {
		t.Fatalf("new_session: %s", resultText(result))
	}
	if len(gotDirs) != 1 || gotDirs[0] != remoteOwnedDir {
		t.Fatalf("git context read for %v; want exactly [%s]", gotDirs, remoteOwnedDir)
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one prompt, got %d", len(platform.sentMessages))
	}
	prompt := platform.sentMessages[0].Message
	if !strings.Contains(prompt, "owner-branch") || !strings.Contains(prompt, "owner.go") {
		t.Errorf("prompt missing the owner's git context: %q", prompt)
	}
}

// TestNewSession_OmitsGitContextWithoutHostSeam proves the enrichment is
// dropped rather than read from this machine when no owner-scoped reader
// is available.
func TestNewSession_OmitsGitContextWithoutHostSeam(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-noctx", Title: "parent", Directory: remoteOwnedDir, TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-noctx"}

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		OcDB:       ocDB,
		StateDB:    stateDB,
		Platform:   platform,
		PlatformID: "opencode",
	})

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-noctx",
		"intent":     "look around",
	})
	if result.IsError {
		t.Fatalf("new_session: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one prompt, got %d", len(platform.sentMessages))
	}
	prompt := platform.sentMessages[0].Message
	for _, section := range []string{"## Current Branch", "## Uncommitted Changes"} {
		if strings.Contains(prompt, section) {
			t.Errorf("prompt kept %q without an owner-scoped git read: %q", section, prompt)
		}
	}
}

// TestCancelSession_RoutesTmuxKillToTheOwningHost pins that killing a
// legacy child's tmux target goes through the owner-routed host seam
// (with the child's worktree as the owner key), not a direct tmux call on
// this machine.
func TestCancelSession_RoutesTmuxKillToTheOwningHost(t *testing.T) {
	stateDB := openTestStateDB(t)
	worktree := remoteOwnedDir + "/.worktrees/feat"
	seedCancellableChild(t, stateDB, "child-remote-tmux", worktree)

	var gotDir, gotTarget string
	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		StateDB:    stateDB,
		Platform:   &fakePlatformForTools{},
		PlatformID: "opencode",
		KillTmuxTarget: internalmcp.TmuxTargetKiller(func(_ context.Context, dir, target string) error {
			gotDir, gotTarget = dir, target
			return nil
		}),
	})

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "child-remote-tmux",
	})
	if result.IsError {
		t.Fatalf("cancel_session: %s", resultText(result))
	}
	if gotDir != worktree || gotTarget != "some-session:1" {
		t.Fatalf("tmux kill routed with dir=%q target=%q; want dir=%q target=some-session:1", gotDir, gotTarget, worktree)
	}
	assertChildCancelled(t, stateDB, "child-remote-tmux")
}

// TestCancelSession_WithoutHostSeamStillCancels proves the kill is
// skipped (never run on this machine) when no owner-routed killer is
// wired, while the child is still moved to a terminal state.
func TestCancelSession_WithoutHostSeamStillCancels(t *testing.T) {
	stateDB := openTestStateDB(t)
	seedCancellableChild(t, stateDB, "child-no-killer", remoteOwnedDir+"/.worktrees/feat")

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		StateDB:    stateDB,
		Platform:   &fakePlatformForTools{},
		PlatformID: "opencode",
	})

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "child-no-killer",
	})
	if result.IsError {
		t.Fatalf("cancel_session: %s", resultText(result))
	}
	assertChildCancelled(t, stateDB, "child-no-killer")
}

// TestCancelSession_ReportsARefusedTmuxKill pins that a kill the owning
// host refused is visible in the result. The child is marked terminal
// either way, so a retry short-circuits as idempotent success — if this
// call claimed unqualified success the pane would be leaked with nothing
// anywhere saying so.
func TestCancelSession_ReportsARefusedTmuxKill(t *testing.T) {
	stateDB := openTestStateDB(t)
	worktree := remoteOwnedDir + "/.worktrees/feat"
	seedCancellableChild(t, stateDB, "child-kill-refused", worktree)

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		StateDB:    stateDB,
		Platform:   &fakePlatformForTools{},
		PlatformID: "opencode",
		KillTmuxTarget: internalmcp.TmuxTargetKiller(func(context.Context, string, string) error {
			return errors.New("not supported for remote-owned sessions (owner r1)")
		}),
	})

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "child-kill-refused",
	})
	if result.IsError {
		t.Fatalf("cancel_session: %s", resultText(result))
	}
	text := resultText(result)
	for _, want := range []string{`"success": false`, "not supported for remote-owned sessions", "some-session:1"} {
		if !strings.Contains(text, want) {
			t.Errorf("result %s missing %q", text, want)
		}
	}
	// The state transition still happened: leaving the child non-terminal
	// would strand it forever.
	assertChildCancelled(t, stateDB, "child-kill-refused")
}

// TestCancelSession_ReportsASkippedTmuxKill pins the same for the no-
// adapter case: nothing was killed, so nothing may claim it was.
func TestCancelSession_ReportsASkippedTmuxKill(t *testing.T) {
	stateDB := openTestStateDB(t)
	seedCancellableChild(t, stateDB, "child-kill-skipped", remoteOwnedDir+"/.worktrees/feat")

	srv := buildMCPServerWithDeps(t, internalmcp.Deps{
		StateDB:    stateDB,
		Platform:   &fakePlatformForTools{},
		PlatformID: "opencode",
	})

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "child-kill-skipped",
	})
	if result.IsError {
		t.Fatalf("cancel_session: %s", resultText(result))
	}
	if text := resultText(result); !strings.Contains(text, "skipped") {
		t.Errorf("result %s does not report the skipped kill", text)
	}
	assertChildCancelled(t, stateDB, "child-kill-skipped")
}

// seedCancellableChild inserts a running child that carries a legacy tmux
// target, the only kind cancel_session has a target to kill.
func seedCancellableChild(t *testing.T, stateDB *state.DB, id, worktree string) {
	t.Helper()
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              id,
		Platform:        "opencode",
		ParentSessionID: "parent-remote",
		Intent:          "x",
		WorktreePath:    worktree,
		TmuxTarget:      "some-session:1",
		Status:          "running",
		CreatedAt:       1,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}
}

func assertChildCancelled(t *testing.T, stateDB *state.DB, id string) {
	t.Helper()
	cs, err := stateDB.GetChildSession(id)
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.Status != "cancelled" {
		t.Fatalf("child status = %q, want cancelled", cs.Status)
	}
}
