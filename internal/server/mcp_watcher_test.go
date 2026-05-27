package server

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// openWatcherTestStateDB creates an in-memory state.DB for watcher tests.
func openWatcherTestStateDB(t *testing.T) *state.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test state db: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	sdb, err := state.OpenFromSQL(sqlDB)
	if err != nil {
		t.Fatalf("initializing state schema: %v", err)
	}
	return sdb
}

// insertWatcherChildSession is a test helper to insert a child session.
func insertWatcherChildSession(t *testing.T, sdb *state.DB, id, parentID, status string) {
	t.Helper()
	cs := state.ChildSession{
		ID:              id,
		Platform:        "opencode",
		ParentSessionID: parentID,
		Intent:          "test intent",
		ComposedPrompt:  "## Task\ntest\n",
		Status:          status,
		CreatedAt:       time.Now().UnixMilli(),
	}
	if err := sdb.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession %s: %v", id, err)
	}
}

func TestBuildInjectionMessage_Completed(t *testing.T) {
	cs := state.ChildSession{
		ID:     "child-1",
		Intent: "fix the linting issue",
	}
	msg := buildInjectionMessage(cs, "completed", "Fixed 3 lint errors.")
	if msg == "" {
		t.Fatal("expected non-empty message")
	}
	for _, want := range []string{"fix the linting issue", "child-1", "Fixed 3 lint errors."} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q: %q", want, msg)
		}
	}
}

func TestBuildInjectionMessage_Error(t *testing.T) {
	cs := state.ChildSession{
		ID:     "child-2",
		Intent: "add tests",
	}
	msg := buildInjectionMessage(cs, "error", "compilation failed")
	if !strings.Contains(msg, "compilation failed") {
		t.Errorf("expected error detail in message: %q", msg)
	}
}

func TestBuildInjectionMessage_WithWorktree(t *testing.T) {
	cs := state.ChildSession{
		ID:           "child-3",
		Intent:       "refactor",
		WorktreePath: "/tmp/worktrees/repo/refactor",
	}
	msg := buildInjectionMessage(cs, "completed", "")
	if !strings.Contains(msg, "/tmp/worktrees/repo/refactor") {
		t.Errorf("expected worktree path in message: %q", msg)
	}
}

func TestIsTerminalStatus(t *testing.T) {
	tests := []struct {
		status   string
		terminal bool
	}{
		{"completed", true},
		{"error", true},
		{"cancelled", true},
		{"starting", false},
		{"running", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isTerminalStatus(tt.status); got != tt.terminal {
			t.Errorf("isTerminalStatus(%q) = %v, want %v", tt.status, got, tt.terminal)
		}
	}
}

func TestCheckAndInjectChildResults_NoStateDB(t *testing.T) {
	// Should not panic when stateDB is nil.
	s := &Server{}
	s.checkAndInjectChildResults(context.Background())
}

func TestCheckAndInjectChildResults_NoChildren(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	s := &Server{stateDB: sdb}
	// Should be a no-op when there are no non-terminal children.
	s.checkAndInjectChildResults(context.Background())
}

func TestCheckAndInjectChildResults_UpdatesStatus(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	insertWatcherChildSession(t, sdb, "child-watch-1", "parent-1", "starting")

	// Build a fake platform that:
	// - owns "child-watch-1" (so PlatformForSession finds it)
	// - returns a "waiting" session detail for it
	// - owns "parent-1" (so injectResultIntoParent finds it)
	// - records SendMessage calls
	var sentMessages []platforms.SendMessageRequest
	fp := &fakePlatform{
		id: "opencode",
		sessions: []db.Session{
			{ID: "child-watch-1", Status: "waiting", MessageCount: 5, Title: "Fix lint"},
			{ID: "parent-1", Status: "waiting"},
		},
	}
	fp.sendMessageFn = func(req platforms.SendMessageRequest) error {
		sentMessages = append(sentMessages, req)
		return nil
	}
	fp.sessionDetailFn = func(id string) (*platforms.SessionDetail, error) {
		for _, s := range fp.sessions {
			if s.ID == id {
				sess := s
				return &platforms.SessionDetail{Session: &sess}, nil
			}
		}
		return nil, platforms.ErrNotFound
	}

	reg := platforms.NewRegistry()
	reg.Register(fp)

	s := &Server{
		stateDB:  sdb,
		registry: reg,
	}

	s.checkAndInjectChildResults(context.Background())

	// Verify the child session status was updated.
	cs, err := sdb.GetChildSession("child-watch-1")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.Status != "completed" {
		t.Errorf("expected status=completed, got %q", cs.Status)
	}
	if cs.CompletedAt == 0 {
		t.Error("expected CompletedAt to be set")
	}

	// Verify the result was injected into the parent session.
	if len(sentMessages) != 1 {
		t.Fatalf("expected 1 injected message, got %d", len(sentMessages))
	}
	if sentMessages[0].SessionID != "parent-1" {
		t.Errorf("message sent to wrong session: %q", sentMessages[0].SessionID)
	}
	if !strings.Contains(sentMessages[0].Message, "Fix lint") {
		t.Errorf("message missing session title: %q", sentMessages[0].Message)
	}
}

func TestCheckAndInjectChildResults_AlreadyTerminal(t *testing.T) {
	sdb := openWatcherTestStateDB(t)
	insertWatcherChildSession(t, sdb, "child-done", "parent-1", "completed")

	s := &Server{stateDB: sdb}
	// Should be a no-op: completed sessions are not in the non-terminal list.
	s.checkAndInjectChildResults(context.Background())
}
