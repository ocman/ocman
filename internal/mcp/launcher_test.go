package mcp

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worktree"
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
}

func (f *fakePlatformAdapter) CreateSession(_ context.Context, req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
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
func noopWorktreeCreator(_ context.Context, req worktree.CreateRequest) (*worktree.CreateResult, error) {
	return &worktree.CreateResult{
		Path:   "/tmp/worktrees/repo/" + req.Branch,
		Branch: req.Branch,
		Reused: false,
	}, nil
}

// noopTmuxLauncher is a tmuxLauncher that always succeeds.
func noopTmuxLauncher(_, _ string) (string, bool, error) {
	return "~/src/repo:wt-branch", true, nil
}

// immediatePortDiscoverer returns a port immediately (simulates a fast startup).
func immediatePortDiscoverer(_ string) string {
	return "12345"
}

func TestLaunch_CreatesSessionAndSendsPrompt(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{createSessionID: "child-abc"}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopTmuxLauncher, immediatePortDiscoverer)

	childID, err := launcher.Launch(context.Background(), LaunchRequest{
		ParentSessionID: "parent-1",
		Platform:        "opencode",
		Directory:       "/repo",
		Intent:          "fix lint",
		ComposedPrompt:  "## Task\nfix lint\n",
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

func TestLaunch_CreateSessionError(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{
		createSessionErr: errors.New("no running opencode instance"),
	}

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopTmuxLauncher, immediatePortDiscoverer)

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

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopTmuxLauncher, immediatePortDiscoverer)

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

	launcher := NewSessionLauncher(db, platform, noopWorktreeCreator, noopTmuxLauncher, immediatePortDiscoverer)

	childID, wtResult, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{
			ParentSessionID: "parent-1",
			Platform:        "opencode",
			Intent:          "fix lint in worktree",
			ComposedPrompt:  "## Task\nfix lint\n",
		},
		worktree.CreateRequest{
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
	if cs.TmuxTarget == "" {
		t.Error("expected TmuxTarget to be set")
	}
}

func TestLaunchWithWorktree_WorktreeCreateError(t *testing.T) {
	db := openTestStateDB(t)
	platform := &fakePlatformAdapter{}

	failingCreator := func(_ context.Context, _ worktree.CreateRequest) (*worktree.CreateResult, error) {
		return nil, errors.New("branch already checked out")
	}

	launcher := NewSessionLauncher(db, platform, failingCreator, noopTmuxLauncher, immediatePortDiscoverer)

	_, _, err := launcher.LaunchWithWorktree(
		context.Background(),
		LaunchRequest{ParentSessionID: "parent-1", Platform: "opencode"},
		worktree.CreateRequest{RepoRoot: "/repo", Branch: "fix-lint"},
	)
	if err == nil {
		t.Fatal("expected error when worktree creation fails")
	}
}

func TestWaitForPort_Timeout(t *testing.T) {
	// portDiscoverer that never finds a port.
	neverDiscovers := func(_ string) string { return "" }

	launcher := &SessionLauncher{discoverPort: neverDiscovers}

	// Use a very short timeout via context cancellation.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := launcher.waitForPort(ctx, "/repo")
	if err == nil {
		t.Fatal("expected error when port never becomes available")
	}
}
