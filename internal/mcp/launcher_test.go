package mcp

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"

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
	liveRules        []platforms.PermissionRule
	liveRulesErr     error
}

func (f *fakePlatformAdapter) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	f.permReqs = append(f.permReqs, req)
	return f.permErr
}

func (f *fakePlatformAdapter) PermissionRules(_ context.Context, _ string) ([]platforms.PermissionRule, error) {
	return f.liveRules, f.liveRulesErr
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

// hostWorktreeCreator fakes the owning host's CreateWorktreeSession: it
// creates both the worktree and the session rooted at it, exactly as
// hostsvc.Host does on the machine that owns the project.
func hostWorktreeCreator(sessionID string) WorktreeSessionCreator {
	return func(_ context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error) {
		return &WorktreeSessionResult{
			SessionID:    sessionID,
			WorktreePath: "/tmp/worktrees/repo/" + req.Branch,
			Branch:       req.Branch,
		}, nil
	}
}

// noopWorktreeCreator is a WorktreeSessionCreator that always succeeds.
var noopWorktreeCreator = hostWorktreeCreator("child-wt-host")

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
	cs, err := db.GetChildSession(t.Context(), "child-abc")
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

func TestLaunch_ParentlessSessionDoesNotQueueAsyncFeedback(t *testing.T) {
	db := openTestStateDB(t)
	launcher := NewSessionLauncher(db, &fakePlatformAdapter{createSessionID: "standalone"}, noopWorktreeCreator, noopEnsurer)

	if _, err := launcher.Launch(context.Background(), LaunchRequest{Platform: "opencode", Directory: "/repo"}); err != nil {
		t.Fatal(err)
	}
	child, err := db.GetChildSession(t.Context(), "standalone")
	if err != nil || child.ResultDelivery != "detached" {
		t.Fatalf("parentless child = %+v, %v", child, err)
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

// TestLaunch_PermissionError_DoesNotFail confirms a SetPermissionRules
// failure doesn't strand the created session — Launch still succeeds and
// the prompt is still sent.
func TestLaunch_PermissionError_DoesNotFail(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{
		createSessionID: "child-permfail",
		permErr:         errors.New("upstream rejected rules"),
	}
	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopEnsurer)

	id, err := launcher.Launch(context.Background(), LaunchRequest{
		Platform:        "opencode",
		Directory:       "/repo",
		ComposedPrompt:  "go",
		PermissionRules: []platforms.PermissionRule{{Permission: "edit", Pattern: "**", Action: "deny"}},
	})
	if err != nil {
		t.Fatalf("Launch should succeed despite permission failure: %v", err)
	}
	if id != "child-permfail" {
		t.Fatalf("session id = %q; want child-permfail", id)
	}
	if len(platform.sentMessages) != 1 {
		t.Errorf("expected prompt still sent, got %d messages", len(platform.sentMessages))
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
	platform := &fakePlatformAdapter{}

	launcher := NewSessionLauncher(db, platform, hostWorktreeCreator("child-wt-1"), noopEnsurer)

	childID, wtResult, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{
			ParentSessionID: "parent-1",
			Platform:        "opencode",
			Intent:          "fix lint in worktree",
			ComposedPrompt:  "## Task\nfix lint\n",
		},
		WorktreeSessionRequest{
			ParentDir: "/repo",
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
	cs, err := db.GetChildSession(t.Context(), "child-wt-1")
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

// TestLaunchWithWorktree_DelegatesEverythingToTheOwningHost pins AD-16:
// the worktree, the project's opencode instance and the session itself
// are all created by the owning host in one call, so the launcher must
// not ensure an instance or create a session on the hub. (The host-side
// behaviour — ensure against the repo root, session rooted at the
// worktree — is covered by internal/hostsvc/local's host tests.)
func TestLaunchWithWorktree_DelegatesEverythingToTheOwningHost(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "hub-created-session"}

	ensureCalls := 0
	ensurer := func(_ context.Context, _ string) (string, error) {
		ensureCalls++
		return "9090", nil
	}
	var gotReq WorktreeSessionRequest
	creator := func(_ context.Context, req WorktreeSessionRequest) (*WorktreeSessionResult, error) {
		gotReq = req
		return &WorktreeSessionResult{
			SessionID:    "host-created-session",
			WorktreePath: "/repo/.worktrees/repo/fix",
			Branch:       "fix",
		}, nil
	}
	launcher := NewSessionLauncher(db, platform, creator, ensurer)

	childID, wtResult, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{Platform: "opencode", ComposedPrompt: "go"},
		WorktreeSessionRequest{ParentDir: "/repo", Branch: "fix", NewBranch: true, BaseRef: "main"},
	)
	if err != nil {
		t.Fatalf("LaunchWithWorktree: %v", err)
	}
	if gotReq.ParentDir != "/repo" || gotReq.Branch != "fix" || !gotReq.NewBranch || gotReq.BaseRef != "main" {
		t.Errorf("host request = %+v; want the caller's worktree request verbatim", gotReq)
	}
	if childID != "host-created-session" || wtResult.WorktreePath != "/repo/.worktrees/repo/fix" {
		t.Errorf("child = %q at %q; want the host's session/worktree", childID, wtResult.WorktreePath)
	}
	if ensureCalls != 0 {
		t.Errorf("ensureOpencode called %d times; the owning host ensures its own instance", ensureCalls)
	}
	if platform.createReq.Directory != "" {
		t.Errorf("CreateSession ran on the hub for %q; the owning host creates the session", platform.createReq.Directory)
	}
	// The prompt still goes to the host-created session.
	if len(platform.sentMessages) != 1 || platform.sentMessages[0].SessionID != "host-created-session" {
		t.Errorf("prompt not sent to the host-created session: %+v", platform.sentMessages)
	}
}

// TestLaunchWithWorktree_FailsClosedWithoutHostAdapter proves the split
// refuses rather than falling back to a local git worktree when no
// owner-routed host adapter is wired.
func TestLaunchWithWorktree_FailsClosedWithoutHostAdapter(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{}
	launcher := NewSessionLauncher(db, platform, nil, noopEnsurer)

	_, _, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{Platform: "opencode"},
		WorktreeSessionRequest{ParentDir: "/repo", Branch: "fix"},
	)
	if err == nil {
		t.Fatal("expected a failure when no host adapter is wired")
	}
	if platform.createReq.Directory != "" {
		t.Errorf("created a session anyway for %q", platform.createReq.Directory)
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

	failingCreator := func(_ context.Context, _ WorktreeSessionRequest) (*WorktreeSessionResult, error) {
		return nil, errors.New("branch already checked out")
	}

	launcher := NewSessionLauncher(db, platform, failingCreator, noopEnsurer)

	_, _, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{ParentSessionID: "parent-1", Platform: "opencode"},
		WorktreeSessionRequest{ParentDir: "/repo", Branch: "fix-lint"},
	)
	if err == nil {
		t.Fatal("expected error when worktree creation fails")
	}
}
