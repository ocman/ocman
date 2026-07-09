package mcp

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// openTestStateDB creates an in-memory state.DB for launcher tests.
func openTestStateDB(t *testing.T) *state.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test state db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	db, err := state.OpenFromSQL(sqlDB)
	if err != nil {
		t.Fatalf("initializing state schema: %v", err)
	}
	return db
}

// fakePlatformAdapter implements platformAdapter for tests.
type fakePlatformAdapter struct {
	createSessionID  string
	createSessionErr error
	sendMessageErr   error
	sentMessages     []platforms.SendMessageRequest
	createReq        platforms.CreateSessionRequest
	permReqs         []platforms.SetPermissionRulesRequest
	permErr          error
}

func (f *fakePlatformAdapter) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	f.permReqs = append(f.permReqs, req)
	return f.permErr
}

func (f *fakePlatformAdapter) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	f.createReq = req
	if f.createSessionErr != nil {
		return nil, f.createSessionErr
	}
	id := f.createSessionID
	if id == "" {
		id = "child-session-123"
	}
	return &platforms.CreateSessionResponse{ID: id}, nil
}

func (f *fakePlatformAdapter) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	f.sentMessages = append(f.sentMessages, req)
	return f.sendMessageErr
}

// noopWorktreeCreator is a worktreeCreator that always succeeds.
func noopWorktreeCreator(_ context.Context, req git.CreateWorktreeRequest) (*git.CreateWorktreeResult, error) {
	return &git.CreateWorktreeResult{
		Path:   "/tmp/worktrees/repo/" + req.Branch,
		Branch: req.Branch,
		Reused: false,
	}, nil
}

// noopEnsurer is a ProjectOpencodeEnsurer that always returns a port
// (simulates the project instance already running / launched).
func noopEnsurer(_ context.Context, _ string) (string, error) {
	return "12345", nil
}

func TestLaunch_CreatesSessionAndSendsPrompt(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-abc"}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	childID, err := launcher.Launch(context.Background(), LaunchRequest{
		ParentSessionID: "parent-1",
		Platform:        "opencode",
		Directory:       "/repo",
		Intent:          "fix lint",
		ComposedPrompt:  "## Task\nfix lint\n",
		Model:           "anthropic/claude-haiku-4-5",
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if childID != "child-abc" {
		t.Errorf("expected childID=child-abc, got %q", childID)
	}

	// Verify the prompt was sent.
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(platform.sentMessages))
	}
	if platform.sentMessages[0].SessionID != "child-abc" {
		t.Errorf("message sent to wrong session: %q", platform.sentMessages[0].SessionID)
	}
	if platform.sentMessages[0].Message != "## Task\nfix lint\n" {
		t.Errorf("unexpected message: %q", platform.sentMessages[0].Message)
	}
	if platform.sentMessages[0].Model != "anthropic/claude-haiku-4-5" {
		t.Errorf("unexpected model: %q", platform.sentMessages[0].Model)
	}

	// Verify the child session was persisted.
	cs, err := db.GetChildSession("child-abc")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.ParentSessionID != "parent-1" {
		t.Errorf("ParentSessionID: got %q, want parent-1", cs.ParentSessionID)
	}
	if cs.Status != "starting" {
		t.Errorf("Status: got %q, want starting", cs.Status)
	}
	if cs.Intent != "fix lint" {
		t.Errorf("Intent: got %q, want fix lint", cs.Intent)
	}
}

func TestLaunch_ThreadsAgentReasoningAndPermissions(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-set"}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	rules := []platforms.PermissionRule{
		{Permission: "edit", Pattern: "**", Action: "deny"},
	}
	_, err := launcher.Launch(context.Background(), LaunchRequest{
		ParentSessionID: "parent-1",
		Platform:        "opencode",
		Directory:       "/repo",
		Intent:          "plan work",
		ComposedPrompt:  "## Task\nplan\n",
		Model:           "anthropic/claude-opus-4-8",
		Agent:           "plan",
		Reasoning:       "high",
		PermissionRules: rules,
	})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}

	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected 1 sent message, got %d", len(platform.sentMessages))
	}
	msg := platform.sentMessages[0]
	if msg.Agent != "plan" {
		t.Errorf("Agent: got %q, want plan", msg.Agent)
	}
	if msg.Reasoning != "high" {
		t.Errorf("Reasoning: got %q, want high", msg.Reasoning)
	}

	if len(platform.permReqs) != 1 {
		t.Fatalf("expected 1 SetPermissionRules call, got %d", len(platform.permReqs))
	}
	pr := platform.permReqs[0]
	if pr.SessionID != "child-set" {
		t.Errorf("permission set on wrong session: %q", pr.SessionID)
	}
	if len(pr.Rules) != 1 || pr.Rules[0].Permission != "edit" || pr.Rules[0].Action != "deny" {
		t.Errorf("unexpected permission rules: %+v", pr.Rules)
	}
}

// TestLaunch_NoPermissionRules_SkipsSetPermission confirms the post-create
// permission call is only made when rules are provided.
func TestLaunch_NoPermissionRules_SkipsSetPermission(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-noperm"}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	if _, err := launcher.Launch(context.Background(), LaunchRequest{
		Platform:  "opencode",
		Directory: "/repo",
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if len(platform.permReqs) != 0 {
		t.Errorf("expected no SetPermissionRules calls, got %d", len(platform.permReqs))
	}
}

func TestLaunch_CreateSessionError(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{
		createSessionErr: errors.New("no running opencode instance"),
	}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	_, err := launcher.Launch(context.Background(), LaunchRequest{
		ParentSessionID: "parent-1",
		Platform:        "opencode",
		Directory:       "/repo",
		Intent:          "fix lint",
	})
	if err == nil {
		t.Fatal("expected error when CreateSession fails")
	}
}

func TestLaunch_SendMessageError_DoesNotFail(t *testing.T) {
	// SendMessage failure should not cause Launch to fail — the session
	// was created; the user can still interact with it manually.
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{
		createSessionID: "child-xyz",
		sendMessageErr:  errors.New("platform unreachable"),
	}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	childID, err := launcher.Launch(context.Background(), LaunchRequest{
		ParentSessionID: "parent-1",
		Platform:        "opencode",
		Directory:       "/repo",
		Intent:          "fix lint",
		ComposedPrompt:  "## Task\nfix lint\n",
	})
	if err != nil {
		t.Fatalf("Launch should succeed even when SendMessage fails: %v", err)
	}
	if childID != "child-xyz" {
		t.Errorf("expected childID=child-xyz, got %q", childID)
	}
}

func TestLaunchWithWorktree_Success(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-wt-1"}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	childID, wtResult, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{
			ParentSessionID: "parent-1",
			Platform:        "opencode",
			Intent:          "fix lint in worktree",
			ComposedPrompt:  "## Task\nfix lint\n",
		},
		git.CreateWorktreeRequest{
			RepoRoot:  "/repo",
			Branch:    "fix-lint",
			NewBranch: true,
			BaseRef:   "main",
		},
	)
	if err != nil {
		t.Fatalf("LaunchWithWorktree: %v", err)
	}
	if childID != "child-wt-1" {
		t.Errorf("expected childID=child-wt-1, got %q", childID)
	}
	if wtResult == nil || wtResult.Branch != "fix-lint" {
		t.Errorf("unexpected wtResult: %+v", wtResult)
	}

	// Verify the child session was persisted with worktree fields.
	cs, err := db.GetChildSession("child-wt-1")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.WorktreePath == "" {
		t.Error("expected WorktreePath to be set")
	}
	if cs.Branch != "fix-lint" {
		t.Errorf("Branch: got %q, want fix-lint", cs.Branch)
	}
	// #268: no per-worktree tmux window — TmuxTarget stays empty.
	if cs.TmuxTarget != "" {
		t.Errorf("TmuxTarget should be empty (in-app path): %q", cs.TmuxTarget)
	}
}

// TestLaunchWithWorktree_EnsuresProjectInstance proves LaunchWithWorktree
// ensures the project's single opencode instance against the repo root
// (not the worktree path) and threads that port into CreateSession.
func TestLaunchWithWorktree_EnsuresProjectInstance(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-wt-2"}

	var ensuredDir string
	ensurer := func(_ context.Context, dir string) (string, error) {
		ensuredDir = dir
		return "9090", nil
	}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, ensurer)

	_, wtResult, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{Platform: "opencode", ComposedPrompt: "go"},
		git.CreateWorktreeRequest{RepoRoot: "/repo", Branch: "fix", NewBranch: true, BaseRef: "main"},
	)
	if err != nil {
		t.Fatalf("LaunchWithWorktree: %v", err)
	}
	// Ensured against the repo root, not the worktree path.
	if ensuredDir != "/repo" {
		t.Errorf("ensured dir = %q; want /repo (repo root)", ensuredDir)
	}
	// The session is created rooted at the worktree path on the ensured port.
	if platform.createReq.Directory != wtResult.Path {
		t.Errorf("CreateSession dir = %q; want worktree %q", platform.createReq.Directory, wtResult.Path)
	}
	if platform.createReq.Port != "9090" {
		t.Errorf("CreateSession port = %q; want ensured 9090", platform.createReq.Port)
	}
}

// TestLaunch_EnsuresBeforeCreate proves a same-directory Launch ensures
// the project instance first (self-heal) and threads the port.
func TestLaunch_EnsuresBeforeCreate(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-same"}
	var ensuredDir string
	ensurer := func(_ context.Context, dir string) (string, error) {
		ensuredDir = dir
		return "7788", nil
	}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, ensurer)

	if _, err := launcher.Launch(context.Background(), LaunchRequest{
		Platform:  "opencode",
		Directory: "/proj",
	}); err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if ensuredDir != "/proj" {
		t.Errorf("ensured dir = %q; want /proj", ensuredDir)
	}
	if platform.createReq.Port != "7788" {
		t.Errorf("CreateSession port = %q; want 7788", platform.createReq.Port)
	}
}

// TestLaunch_EnsureError_Fails proves an ensure failure fails the launch
// (the instance couldn't be brought up).
// TestLaunch_EnsureError_FallsBackToDiscovery proves the same-directory
// self-heal contract (spec D-2 / US-10): a failed ensure (e.g. tmux
// absent on the host) must NOT fail the launch. Launch falls through
// with an empty port so CreateSession's own discovery can still find an
// already-running instance. The session is created with port="".
func TestLaunch_EnsureError_FallsBackToDiscovery(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-fallback"}
	ensurer := func(_ context.Context, _ string) (string, error) {
		return "", errors.New("tmux not available")
	}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, ensurer)
	id, err := launcher.Launch(context.Background(), LaunchRequest{
		Platform:  "opencode",
		Directory: "/proj",
	})
	if err != nil {
		t.Fatalf("Launch must self-heal past ensure failure, got: %v", err)
	}
	if id != "child-fallback" {
		t.Fatalf("session id = %q; want child-fallback", id)
	}
	if platform.createReq.Port != "" {
		t.Errorf("CreateSession port = %q; want empty (fallback to discovery)", platform.createReq.Port)
	}
}

// TestLaunch_EnsureFails_CreateFails_Fails confirms Launch still fails
// when the fallback CreateSession itself fails (genuinely no running
// instance) — i.e. it doesn't swallow the real error.
func TestLaunch_EnsureFails_CreateFails_Fails(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionErr: errors.New("platform unreachable")}
	ensurer := func(_ context.Context, _ string) (string, error) {
		return "", errors.New("tmux not available")
	}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, ensurer)
	if _, err := launcher.Launch(context.Background(), LaunchRequest{
		Platform:  "opencode",
		Directory: "/proj",
	}); err == nil {
		t.Fatal("expected error when both ensure and create fail")
	}
}

func TestLaunchWithWorktree_WorktreeCreateError(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{}

	failingCreator := func(_ context.Context, _ git.CreateWorktreeRequest) (*git.CreateWorktreeResult, error) {
		return nil, errors.New("branch already checked out")
	}

	launcher := NewSessionLauncher(db, platform, failingCreator, noopEnsurer)

	_, _, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{ParentSessionID: "parent-1", Platform: "opencode"},
		git.CreateWorktreeRequest{RepoRoot: "/repo", Branch: "fix-lint"},
	)
	if err == nil {
		t.Fatal("expected error when worktree creation fails")
	}
}
