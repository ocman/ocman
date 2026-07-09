package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/gitexec"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// openTestStateDB creates an in-memory state.DB for tool tests.
func openTestStateDB(t *testing.T) *state.DB {
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

// openTestOpenCodeDB creates a file-backed OpenCode DB fixture. The MCP
// server opens this read-only, so tests seed it before handing it to ocman.
func openTestOpenCodeDB(t *testing.T, sessions []db.Session) *db.DB {
	t.Helper()

	path := filepath.Join(t.TempDir(), "opencode.db")
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("opening test opencode db: %v", err)
	}
	_, err = sqlDB.Exec(`
		CREATE TABLE session (
			id TEXT PRIMARY KEY,
			project_id TEXT NOT NULL DEFAULT '',
			parent_id TEXT,
			title TEXT NOT NULL DEFAULT '',
			directory TEXT NOT NULL DEFAULT '',
			time_created INTEGER NOT NULL DEFAULT 0,
			time_updated INTEGER NOT NULL DEFAULT 0,
			summary_additions INTEGER,
			summary_deletions INTEGER,
			summary_files INTEGER,
			share_url TEXT
		);
		CREATE TABLE message (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
		CREATE TABLE part (
			id TEXT PRIMARY KEY,
			message_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}'
		);
	`)
	if err != nil {
		sqlDB.Close()
		t.Fatalf("creating opencode schema: %v", err)
	}
	for _, s := range sessions {
		_, err = sqlDB.Exec(
			`INSERT INTO session (id, project_id, title, directory, time_created, time_updated) VALUES (?, '', ?, ?, ?, ?)`,
			s.ID, s.Title, s.Directory, s.TimeCreated, s.TimeUpdated,
		)
		if err != nil {
			sqlDB.Close()
			t.Fatalf("inserting opencode session %s: %v", s.ID, err)
		}
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("closing setup db: %v", err)
	}

	ocDB, err := db.Open(path)
	if err != nil {
		t.Fatalf("opening readonly opencode db: %v", err)
	}
	t.Cleanup(func() { ocDB.Close() })
	return ocDB
}

// fakePlatformForTools implements platforms.Platform for tool tests.
type fakePlatformForTools struct {
	fakePlatformBase
	createSessionID  string
	createSessionErr error
	sendMessageErr   error
	sentMessages     []platforms.SendMessageRequest
}

func (f *fakePlatformForTools) ID() platforms.ID { return "opencode" }

func (f *fakePlatformForTools) CreateSession(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	if f.createSessionErr != nil {
		return nil, f.createSessionErr
	}
	id := f.createSessionID
	if id == "" {
		id = "child-tool-test"
	}
	return &platforms.CreateSessionResponse{ID: id}, nil
}

func (f *fakePlatformForTools) SendMessage(_ context.Context, req platforms.SendMessageRequest) error {
	f.sentMessages = append(f.sentMessages, req)
	return f.sendMessageErr
}

// fakePlatformBase provides no-op implementations of all Platform methods.
type fakePlatformBase struct{}

func (fakePlatformBase) ID() platforms.ID                     { return "fake" }
func (fakePlatformBase) DisplayName() string                  { return "Fake" }
func (fakePlatformBase) Available(_ context.Context) bool     { return true }
func (fakePlatformBase) Capabilities() platforms.Capabilities { return platforms.Capabilities{} }
func (fakePlatformBase) Sessions(_ context.Context, _ string, _ int64) ([]db.Session, error) {
	return nil, nil
}
func (fakePlatformBase) Session(_ context.Context, _ string, _, _ int) (*platforms.SessionDetail, error) {
	return nil, platforms.ErrNotFound
}
func (fakePlatformBase) Owns(_ context.Context, _ string) bool { return false }
func (fakePlatformBase) SessionsInactiveBefore(_ context.Context, _ int64) ([]db.SessionArchiveCandidate, error) {
	return nil, nil
}
func (fakePlatformBase) SessionChanges(_ context.Context, _ string) (*platforms.SessionChanges, error) {
	return nil, platforms.ErrUnsupported
}
func (fakePlatformBase) SessionInfo(_ context.Context, _ string) (*platforms.SessionInfo, error) {
	return nil, platforms.ErrUnsupported
}
func (fakePlatformBase) LiveStatus(_ string) *platforms.LiveState { return nil }
func (fakePlatformBase) AgentCatalog(_ context.Context, _ string) ([]platforms.AgentCatalogEntry, error) {
	return nil, nil
}
func (fakePlatformBase) SlashCommands(_ context.Context, _ string) ([]platforms.SlashCommandEntry, error) {
	return nil, nil
}
func (fakePlatformBase) SessionModels(_ context.Context, _ string) (*platforms.SessionModelsResponse, error) {
	return nil, nil
}
func (fakePlatformBase) ListPermissions(_ context.Context, _ string) ([]platforms.LivePrompt, error) {
	return nil, nil
}
func (fakePlatformBase) ListQuestions(_ context.Context, _ string) ([]platforms.LivePrompt, error) {
	return nil, nil
}
func (fakePlatformBase) SendMessage(_ context.Context, _ platforms.SendMessageRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) ExecuteCommand(_ context.Context, _ platforms.ExecuteCommandRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) RunShell(_ context.Context, _ platforms.RunShellRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) RespondPermission(_ context.Context, _ platforms.RespondPermissionRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) RespondQuestion(_ context.Context, _ platforms.RespondQuestionRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) RejectQuestion(_ context.Context, _ platforms.RejectQuestionRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) Abort(_ context.Context, _ platforms.AbortRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) RenameSession(_ context.Context, _ platforms.RenameSessionRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) PermissionRules(_ context.Context, _ string) ([]platforms.PermissionRule, error) {
	return nil, platforms.ErrUnsupported
}
func (fakePlatformBase) SetPermissionRules(_ context.Context, _ platforms.SetPermissionRulesRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) Compact(_ context.Context, _ platforms.CompactRequest) error {
	return platforms.ErrUnsupported
}
func (fakePlatformBase) CreateSession(_ context.Context, _ platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
	return nil, platforms.ErrUnsupported
}
func (fakePlatformBase) ProxyEvents(_ context.Context, _ string, _ io.Writer, _ func()) error {
	return platforms.ErrUnsupported
}

// buildTestMCPServer builds an mcptest.Server with fake dependencies.
func buildTestMCPServer(t *testing.T, stateDB *state.DB, platform *fakePlatformForTools) *mcptest.Server {
	return buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, nil)
}

func buildTestMCPServerWithOpenCodeDB(t *testing.T, stateDB *state.DB, platform *fakePlatformForTools, ocDB *db.DB) *mcptest.Server {
	t.Helper()

	fakeWT := internalmcp.WorktreeCreator(func(_ context.Context, req git.CreateWorktreeRequest) (*git.CreateWorktreeResult, error) {
		return &git.CreateWorktreeResult{
			Path:   "/tmp/worktrees/repo/" + req.Branch,
			Branch: req.Branch,
		}, nil
	})
	fakeEnsure := internalmcp.ProjectOpencodeEnsurer(func(_ context.Context, _ string) (string, error) {
		return "12345", nil
	})

	deps := internalmcp.Deps{
		OcDB:                  ocDB,
		StateDB:               stateDB,
		Platform:              platform,
		PlatformID:            "opencode",
		CreateWorktree:        fakeWT,
		EnsureProjectOpencode: fakeEnsure,
	}

	tools := internalmcp.ServerTools(deps)
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatalf("mcptest.NewServer: %v", err)
	}
	t.Cleanup(srv.Close)
	return srv
}

// callTool calls a tool via the mcptest client.
func callTool(t *testing.T, srv *mcptest.Server, toolName string, args map[string]interface{}) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	result, err := srv.Client().CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", toolName, err)
	}
	return result
}

// resultText extracts the text content from a CallToolResult.
func resultText(result *mcplib.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(mcplib.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestGetSessionStatus_NotFound(t *testing.T) {
	stateDB := openTestStateDB(t)
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "get_session_status", map[string]interface{}{
		"child_session_id": "nonexistent",
	})
	if !result.IsError {
		t.Error("expected error result for nonexistent session")
	}
}

func TestListChildSessions_Empty(t *testing.T) {
	stateDB := openTestStateDB(t)
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "list_child_sessions", map[string]interface{}{
		"session_id": "parent-with-no-children",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	text := resultText(result)
	var arr []interface{}
	if err := json.Unmarshal([]byte(text), &arr); err != nil {
		t.Errorf("expected JSON array, got: %q (err: %v)", text, err)
	}
}

func TestGetCurrentSessionID_ReturnsMostRecentSession(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "ses_old", Title: "old", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
		{ID: "ses_new", Title: "new", Directory: "/repo", TimeCreated: 3000, TimeUpdated: 4000},
	})
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "get_current_session_id", map[string]interface{}{})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}

	var got struct {
		SessionID     string `json:"session_id"`
		Directory     string `json:"directory"`
		SelectionMode string `json:"selection_mode"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &got); err != nil {
		t.Fatalf("expected JSON object: %v", err)
	}
	if got.SessionID != "ses_new" {
		t.Fatalf("expected newest session ID, got %q", got.SessionID)
	}
	if got.Directory != "/repo" {
		t.Fatalf("expected directory /repo, got %q", got.Directory)
	}
	if got.SelectionMode != "most_recent_session" {
		t.Fatalf("expected selection mode, got %q", got.SelectionMode)
	}
}

func TestGetCurrentSessionID_FiltersByDirectory(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "ses_other", Title: "other", Directory: "/other", TimeCreated: 1000, TimeUpdated: 9000},
		{ID: "ses_repo", Title: "repo", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "get_current_session_id", map[string]interface{}{
		"directory": "/repo",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}

	var got struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &got); err != nil {
		t.Fatalf("expected JSON object: %v", err)
	}
	if got.SessionID != "ses_repo" {
		t.Fatalf("expected directory-filtered session ID, got %q", got.SessionID)
	}
}

func TestCancelSession_NotFound(t *testing.T) {
	stateDB := openTestStateDB(t)
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "nonexistent",
	})
	if !result.IsError {
		t.Error("expected error result for nonexistent session")
	}
}

func TestCancelSession_AlreadyTerminal(t *testing.T) {
	stateDB := openTestStateDB(t)

	cs := state.ChildSession{
		ID:              "child-done",
		Platform:        "opencode",
		ParentSessionID: "parent-1",
		Intent:          "fix lint",
		ComposedPrompt:  "## Task\nfix lint\n",
		Status:          "completed",
		CreatedAt:       1000,
	}
	if err := stateDB.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "cancel_session", map[string]interface{}{
		"child_session_id": "child-done",
	})
	if result.IsError {
		t.Fatalf("cancel of terminal session should succeed: %s", resultText(result))
	}
	text := resultText(result)
	if !strings.Contains(text, "true") {
		t.Errorf("expected success=true, got: %q", text)
	}
}

func TestGetSessionStatus_ExistingSession(t *testing.T) {
	stateDB := openTestStateDB(t)

	cs := state.ChildSession{
		ID:              "child-status-test",
		Platform:        "opencode",
		ParentSessionID: "parent-1",
		Intent:          "refactor auth",
		ComposedPrompt:  "## Task\nrefactor auth\n",
		Status:          "running",
		CreatedAt:       2000,
	}
	if err := stateDB.InsertChildSession(cs); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "get_session_status", map[string]interface{}{
		"child_session_id": "child-status-test",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	text := resultText(result)
	if !strings.Contains(text, "running") {
		t.Errorf("expected status=running in result: %q", text)
	}
	if !strings.Contains(text, "refactor auth") {
		t.Errorf("expected intent in result: %q", text)
	}
}

func TestListChildSessions_WithChildren(t *testing.T) {
	stateDB := openTestStateDB(t)

	for i, intent := range []string{"fix lint", "add tests"} {
		cs := state.ChildSession{
			ID:              fmt.Sprintf("child-%d", i),
			Platform:        "opencode",
			ParentSessionID: "parent-list-test",
			Intent:          intent,
			ComposedPrompt:  "## Task\n" + intent + "\n",
			Status:          "starting",
			CreatedAt:       int64(1000 + i),
		}
		if err := stateDB.InsertChildSession(cs); err != nil {
			t.Fatalf("InsertChildSession: %v", err)
		}
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "list_child_sessions", map[string]interface{}{
		"session_id": "parent-list-test",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	text := resultText(result)
	if !strings.Contains(text, "fix lint") || !strings.Contains(text, "add tests") {
		t.Errorf("expected both intents in result: %q", text)
	}
}

func TestSendMessageToChild_DeliversToLinkedChild(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-comm-test",
		Platform:        "opencode",
		ParentSessionID: "parent-comm-test",
		Intent:          "inspect logs",
		ComposedPrompt:  "inspect logs",
		Status:          "running",
		CreatedAt:       1000,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "send_message_to_child", map[string]interface{}{
		"session_id":       "parent-comm-test",
		"child_session_id": "child-comm-test",
		"message":          "What did you find?",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one sent message, got %d", len(platform.sentMessages))
	}
	msg := platform.sentMessages[0]
	if msg.SessionID != "child-comm-test" {
		t.Fatalf("expected message to child session, got %q", msg.SessionID)
	}
	if !strings.Contains(msg.Message, "Message from parent session parent-comm-test") || !strings.Contains(msg.Message, "What did you find?") {
		t.Fatalf("unexpected delivered message: %q", msg.Message)
	}
}

func TestSendMessageToChild_RejectsUnlinkedChild(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-other-parent",
		Platform:        "opencode",
		ParentSessionID: "actual-parent",
		Intent:          "inspect logs",
		ComposedPrompt:  "inspect logs",
		Status:          "running",
		CreatedAt:       1000,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "send_message_to_child", map[string]interface{}{
		"session_id":       "wrong-parent",
		"child_session_id": "child-other-parent",
		"message":          "hello",
	})
	if !result.IsError {
		t.Fatalf("expected error for unlinked child")
	}
	if len(platform.sentMessages) != 0 {
		t.Fatalf("expected no sent messages, got %d", len(platform.sentMessages))
	}
}

func TestNewSession_PassesModelAndUsesParentDir(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-ns", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-ns"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-ns",
		"intent":     "review the diff",
		"model":      "anthropic/claude-haiku-4-5",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one sent message, got %d", len(platform.sentMessages))
	}
	if platform.sentMessages[0].Model != "anthropic/claude-haiku-4-5" {
		t.Fatalf("model not forwarded to child, got %q", platform.sentMessages[0].Model)
	}

	cs, err := stateDB.GetChildSession("child-ns")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.WorktreePath != "" {
		t.Fatalf("expected no worktree for default new_session, got %q", cs.WorktreePath)
	}
}

func TestNewSession_WorktreeRequiresBranch(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-wt", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-wt",
		"intent":     "build a feature",
		"worktree":   true,
	})
	if !result.IsError {
		t.Fatalf("expected error when worktree=true without branch")
	}
}

func TestNewSession_WorktreeCreatesWithModel(t *testing.T) {
	repoDir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		// Strip GIT_DIR / GIT_INDEX_FILE etc. so this git init operates
		// on repoDir, not the ambient repo when run inside a git hook.
		cmd.Env = gitexec.CleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-wt2", Title: "parent", Directory: repoDir, TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-wt-ns"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-wt2",
		"intent":     "build a feature",
		"worktree":   true,
		"branch":     "feat-x",
		"model":      "openai/gpt-5",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 || platform.sentMessages[0].Model != "openai/gpt-5" {
		t.Fatalf("model not forwarded to worktree child: %+v", platform.sentMessages)
	}
	cs, err := stateDB.GetChildSession("child-wt-ns")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if cs.Branch != "feat-x" || cs.WorktreePath == "" {
		t.Fatalf("expected worktree child with branch, got %+v", cs)
	}
}

func TestSendMessageToParent_DeliversToParent(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-to-parent-test",
		Platform:        "opencode",
		ParentSessionID: "parent-to-parent-test",
		Intent:          "inspect logs",
		ComposedPrompt:  "inspect logs",
		Status:          "running",
		CreatedAt:       1000,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)

	result := callTool(t, srv, "send_message_to_parent", map[string]interface{}{
		"child_session_id": "child-to-parent-test",
		"message":          "I found the failing test.",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one sent message, got %d", len(platform.sentMessages))
	}
	msg := platform.sentMessages[0]
	if msg.SessionID != "parent-to-parent-test" {
		t.Fatalf("expected message to parent session, got %q", msg.SessionID)
	}
	if !strings.Contains(msg.Message, "Message from child session child-to-parent-test") || !strings.Contains(msg.Message, "I found the failing test.") {
		t.Fatalf("unexpected delivered message: %q", msg.Message)
	}
}
