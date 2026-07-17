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
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	mcpserver "github.com/mark3labs/mcp-go/server"
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
	return openTestOpenCodeDBWithMessages(t, sessions, nil)
}

// seedMessage is a message row to insert into the test OpenCode DB,
// keyed by session with raw JSON data (e.g. `{"role":"assistant","modelID":"x"}`).
type seedMessage struct {
	sessionID   string
	timeCreated int64
	data        string
}

// openTestOpenCodeDBWithMessages is openTestOpenCodeDB plus message rows,
// so tests can exercise per-session model inheritance.
func openTestOpenCodeDBWithMessages(t *testing.T, sessions []db.Session, messages []seedMessage) *db.DB {
	return openTestOpenCodeDBWithMessagesAndParts(t, sessions, messages, nil)
}

type seedPart struct {
	id          string
	messageID   string
	sessionID   string
	timeCreated int64
	data        string
}

func openTestOpenCodeDBWithMessagesAndParts(t *testing.T, sessions []db.Session, messages []seedMessage, parts []seedPart) *db.DB {
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
			`INSERT INTO session (id, project_id, parent_id, title, directory, time_created, time_updated) VALUES (?, '', NULLIF(?, ''), ?, ?, ?, ?)`,
			s.ID, s.ParentID, s.Title, s.Directory, s.TimeCreated, s.TimeUpdated,
		)
		if err != nil {
			sqlDB.Close()
			t.Fatalf("inserting opencode session %s: %v", s.ID, err)
		}
	}
	for i, m := range messages {
		_, err = sqlDB.Exec(
			`INSERT INTO message (id, session_id, time_created, data) VALUES (?, ?, ?, ?)`,
			fmt.Sprintf("msg-%d", i), m.sessionID, m.timeCreated, m.data,
		)
		if err != nil {
			sqlDB.Close()
			t.Fatalf("inserting opencode message %d: %v", i, err)
		}
	}
	for _, p := range parts {
		_, err = sqlDB.Exec(
			`INSERT INTO part (id, message_id, session_id, time_created, data) VALUES (?, ?, ?, ?, ?)`,
			p.id, p.messageID, p.sessionID, p.timeCreated, p.data,
		)
		if err != nil {
			sqlDB.Close()
			t.Fatalf("inserting opencode part %s: %v", p.id, err)
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
	sendMessageFn    func(platforms.SendMessageRequest)
	sentMessages     []platforms.SendMessageRequest
	permReqs         []platforms.SetPermissionRulesRequest
	liveRules        []platforms.PermissionRule
}

func (f *fakePlatformForTools) ID() platforms.ID { return "opencode" }

func (f *fakePlatformForTools) SetPermissionRules(_ context.Context, req platforms.SetPermissionRulesRequest) error {
	f.permReqs = append(f.permReqs, req)
	return nil
}

func (f *fakePlatformForTools) PermissionRules(_ context.Context, _ string) ([]platforms.PermissionRule, error) {
	return f.liveRules, nil
}

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
	if f.sendMessageFn != nil {
		f.sendMessageFn(req)
	}
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
func (fakePlatformBase) ForkSession(_ context.Context, _ platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	return nil, platforms.ErrUnsupported
}
func (fakePlatformBase) MoveSession(_ context.Context, _ platforms.MoveSessionRequest) error {
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
	return buildTestMCPServerWithResults(t, stateDB, platform, ocDB, nil)
}

func buildTestMCPServerWithResults(t *testing.T, stateDB *state.DB, platform *fakePlatformForTools, ocDB *db.DB, results *internalmcp.ChildResultBroker) *mcptest.Server {
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
		ChildResults:          results,
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

func TestGetCurrentSessionID_ReturnsCallingSessionInsteadOfMostRecentlyUpdated(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDBWithMessagesAndParts(t, []db.Session{
		{ID: "ses_caller", ParentID: "ses_parent", Title: "caller", Directory: "/repo", TimeCreated: 3000, TimeUpdated: 4000},
		{ID: "ses_other", Title: "other active session", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 9000},
	}, []seedMessage{
		{sessionID: "ses_caller", timeCreated: 5000, data: `{"role":"assistant"}`},
	}, []seedPart{
		{
			id: "prt_current", messageID: "msg-0", sessionID: "ses_caller", timeCreated: 5000,
			data: `{"type":"tool","tool":"ocman_get_current_session_id","state":{"status":"running","input":{"directory":"/repo"},"time":{"start":5000}}}`,
		},
		{
			id: "prt_stale", messageID: "msg-0", sessionID: "ses_caller", timeCreated: 4000,
			data: `{"type":"tool","tool":"ocman_get_current_session_id","state":{"status":"running","input":{"directory":"/repo"},"time":{"start":4000}}}`,
		},
	})
	platform := &fakePlatformForTools{}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "get_current_session_id", map[string]interface{}{"directory": "/repo"})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	var got struct {
		SessionID     string `json:"session_id"`
		SelectionMode string `json:"selection_mode"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &got); err != nil {
		t.Fatalf("expected JSON object: %v", err)
	}
	if got.SessionID != "ses_caller" {
		t.Fatalf("expected calling session ID, got %q", got.SessionID)
	}
	if got.SelectionMode != "calling_session" {
		t.Fatalf("expected calling_session selection mode, got %q", got.SelectionMode)
	}
}

func TestGetCurrentSessionID_RejectsAmbiguousConcurrentCallers(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDBWithMessagesAndParts(t, []db.Session{
		{ID: "ses_a", Title: "a", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 1000},
		{ID: "ses_b", Title: "b", Directory: "/repo", TimeCreated: 2000, TimeUpdated: 2000},
	}, []seedMessage{
		{sessionID: "ses_a", timeCreated: 3000, data: `{"role":"assistant"}`},
		{sessionID: "ses_b", timeCreated: 3001, data: `{"role":"assistant"}`},
	}, []seedPart{
		{id: "prt_a", messageID: "msg-0", sessionID: "ses_a", timeCreated: 3000, data: `{"type":"tool","tool":"ocman_get_current_session_id","state":{"status":"running"}}`},
		{id: "prt_b", messageID: "msg-1", sessionID: "ses_b", timeCreated: 3001, data: `{"type":"tool","tool":"ocman_get_current_session_id","state":{"status":"running"}}`},
	})
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, &fakePlatformForTools{}, ocDB)

	result := callTool(t, srv, "get_current_session_id", map[string]interface{}{"directory": "/repo"})
	if !result.IsError || !strings.Contains(resultText(result), "multiple sessions") {
		t.Fatalf("expected ambiguous caller error, got %q", resultText(result))
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

func TestNewSession_ReturnsChildResult(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-result", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	results := internalmcp.NewChildResultBroker()
	platform := &fakePlatformForTools{createSessionID: "child-result"}
	platform.sendMessageFn = func(_ platforms.SendMessageRequest) {
		if !results.Deliver("child-result", internalmcp.ChildResult{Status: "completed", Summary: "Reviewed the diff."}) {
			t.Error("child result had no waiting MCP call")
		}
	}
	srv := buildTestMCPServerWithResults(t, stateDB, platform, ocDB, results)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-result",
		"intent":     "review the diff",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	var got struct {
		Status  string `json:"status"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &got); err != nil {
		t.Fatalf("decoding result: %v", err)
	}
	if got.Status != "completed" || got.Summary != "Reviewed the diff." {
		t.Fatalf("unexpected child result: %+v", got)
	}
}

func TestAwaitSessionResult_ResumesDisconnectedChildWithoutSendingPrompt(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-resume",
		Platform:        "opencode",
		ParentSessionID: "parent-resume",
		Intent:          "review the diff",
		Status:          "completed",
		CreatedAt:       1000,
		ResultDelivery:  "disconnected",
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}
	if err := stateDB.UpdateChildSession("child-resume", "completed", "Original child result.", 2000); err != nil {
		t.Fatalf("UpdateChildSession: %v", err)
	}
	platform := &fakePlatformForTools{}
	results := internalmcp.NewChildResultBroker()
	srv := buildTestMCPServerWithResults(t, stateDB, platform, nil, results)

	result := callTool(t, srv, "await_session_result", map[string]interface{}{
		"session_id": "parent-resume",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if !strings.Contains(resultText(result), "Original child result.") {
		t.Fatalf("missing original result: %s", resultText(result))
	}
	if len(platform.sentMessages) != 0 {
		t.Fatalf("resume sent %d new prompts", len(platform.sentMessages))
	}
}

func TestAwaitSessionResult_ReconnectsRunningChild(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-running",
		Platform:        "opencode",
		ParentSessionID: "parent-running",
		Intent:          "run checks",
		Status:          "running",
		CreatedAt:       1000,
		ResultDelivery:  "disconnected",
	}); err != nil {
		t.Fatal(err)
	}
	results := internalmcp.NewChildResultBroker()
	progress := make(chan mcplib.JSONRPCNotification, 1)
	observedProgress := make(chan mcplib.JSONRPCNotification, 1)
	go func() {
		notification := <-progress
		observedProgress <- notification
		results.Deliver("child-running", internalmcp.ChildResult{Status: "completed", Summary: "Checks passed."})
	}()
	tools := internalmcp.ServerTools(internalmcp.Deps{
		StateDB:      stateDB,
		Platform:     &fakePlatformForTools{},
		PlatformID:   "opencode",
		ChildResults: results,
	})
	mcpServer := mcpserver.NewMCPServer("test", "1.0.0")
	mcpServer.AddTools(tools...)
	httpServer := mcpserver.NewTestStreamableHTTPServer(mcpServer)
	t.Cleanup(httpServer.Close)
	client, err := mcpclient.NewStreamableHttpClient(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Initialize(context.Background(), mcplib.InitializeRequest{Params: mcplib.InitializeParams{
		ProtocolVersion: mcplib.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcplib.Implementation{Name: "test", Version: "1.0.0"},
	}}); err != nil {
		t.Fatal(err)
	}
	client.OnNotification(func(notification mcplib.JSONRPCNotification) {
		if notification.Method == "notifications/progress" {
			progress <- notification
		}
	})

	req := mcplib.CallToolRequest{}
	req.Params.Name = "await_session_result"
	req.Params.Meta = &mcplib.Meta{ProgressToken: "child-progress"}
	req.Params.Arguments = map[string]interface{}{
		"session_id":       "parent-running",
		"child_session_id": "child-running",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result, err := client.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.IsError || !strings.Contains(resultText(result), "Checks passed.") {
		t.Fatalf("unexpected resumed result: %s", resultText(result))
	}
	notification := <-observedProgress
	if notification.Params.AdditionalFields["progressToken"] != "child-progress" {
		t.Fatalf("progress token = %#v", notification.Params.AdditionalFields["progressToken"])
	}
	child, err := stateDB.GetChildSession("child-running")
	if err != nil || child.ResultDelivery != "delivered" {
		t.Fatalf("child delivery state = %+v, %v", child, err)
	}
}

func TestAwaitSessionResult_RejectsAmbiguousDisconnectedChildren(t *testing.T) {
	stateDB := openTestStateDB(t)
	for _, childID := range []string{"child-a", "child-b"} {
		if err := stateDB.InsertChildSession(state.ChildSession{
			ID: childID, Platform: "opencode", ParentSessionID: "parent-many",
			Intent: "task", Status: "running", CreatedAt: 1000, ResultDelivery: "disconnected",
		}); err != nil {
			t.Fatal(err)
		}
	}
	srv := buildTestMCPServerWithResults(t, stateDB, &fakePlatformForTools{}, nil, internalmcp.NewChildResultBroker())

	result := callTool(t, srv, "await_session_result", map[string]interface{}{"session_id": "parent-many"})
	if !result.IsError || !strings.Contains(resultText(result), "multiple disconnected") {
		t.Fatalf("unexpected ambiguity result: %s", resultText(result))
	}
}

func TestAwaitSessionResult_RejectsInvalidRecoveryTargets(t *testing.T) {
	tests := []struct {
		name      string
		parentID  string
		childID   string
		child     *state.ChildSession
		wantError string
	}{
		{name: "missing parent", wantError: "session_id is required"},
		{name: "no disconnected child", parentID: "parent", wantError: "no disconnected child"},
		{name: "unknown explicit child", parentID: "parent", childID: "missing", wantError: "child session not found"},
		{
			name: "wrong parent", parentID: "other", childID: "child", wantError: "does not belong",
			child: &state.ChildSession{ID: "child", ParentSessionID: "parent", Status: "running", ResultDelivery: "disconnected"},
		},
		{
			name: "still connected", parentID: "parent", childID: "child", wantError: "still connected",
			child: &state.ChildSession{ID: "child", ParentSessionID: "parent", Status: "running", ResultDelivery: "waiting"},
		},
		{
			name: "detached launch", parentID: "parent", childID: "child", wantError: "not launched by a resumable call",
			child: &state.ChildSession{ID: "child", ParentSessionID: "parent", Status: "running", ResultDelivery: "detached"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stateDB := openTestStateDB(t)
			if tt.child != nil {
				tt.child.Platform = "opencode"
				tt.child.Intent = "task"
				tt.child.CreatedAt = 1000
				if err := stateDB.InsertChildSession(*tt.child); err != nil {
					t.Fatal(err)
				}
			}
			srv := buildTestMCPServerWithResults(t, stateDB, &fakePlatformForTools{}, nil, internalmcp.NewChildResultBroker())
			args := map[string]interface{}{}
			if tt.parentID != "" {
				args["session_id"] = tt.parentID
			}
			if tt.childID != "" {
				args["child_session_id"] = tt.childID
			}

			result := callTool(t, srv, "await_session_result", args)
			if !result.IsError || !strings.Contains(resultText(result), tt.wantError) {
				t.Fatalf("result = %q, want error containing %q", resultText(result), tt.wantError)
			}
		})
	}
}

func TestNewSession_InheritsParentModelWhenOmitted(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDBWithMessages(t,
		[]db.Session{{ID: "parent-im", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000}},
		[]seedMessage{
			// Older message on a different model; latest must win.
			{sessionID: "parent-im", timeCreated: 1000, data: `{"role":"assistant","providerID":"openai","modelID":"gpt-5"}`},
			{sessionID: "parent-im", timeCreated: 2000, data: `{"role":"assistant","providerID":"anthropic","modelID":"claude-opus-4-8"}`},
		},
	)
	platform := &fakePlatformForTools{createSessionID: "child-im"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-im",
		"intent":     "review the diff",
		// model omitted on purpose
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one sent message, got %d", len(platform.sentMessages))
	}
	if got := platform.sentMessages[0].Model; got != "anthropic/claude-opus-4-8" {
		t.Fatalf("expected child to inherit parent's latest model, got %q", got)
	}
}

func TestNewSession_ExplicitModelOverridesParent(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDBWithMessages(t,
		[]db.Session{{ID: "parent-om", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000}},
		[]seedMessage{
			{sessionID: "parent-om", timeCreated: 2000, data: `{"role":"assistant","providerID":"anthropic","modelID":"claude-opus-4-8"}`},
		},
	)
	platform := &fakePlatformForTools{createSessionID: "child-om"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-om",
		"intent":     "review the diff",
		"model":      "openai/gpt-5-mini",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if got := platform.sentMessages[0].Model; got != "openai/gpt-5-mini" {
		t.Fatalf("explicit model should win over parent, got %q", got)
	}
}

func TestNewSession_WorktreeInheritsParentModel(t *testing.T) {
	repoDir := t.TempDir()
	for _, args := range [][]string{
		{"init"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoDir
		cmd.Env = gitexec.CleanEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDBWithMessages(t,
		[]db.Session{{ID: "parent-wtim", Title: "parent", Directory: repoDir, TimeCreated: 1000, TimeUpdated: 2000}},
		[]seedMessage{
			{sessionID: "parent-wtim", timeCreated: 2000, data: `{"role":"assistant","providerID":"anthropic","modelID":"claude-opus-4-8"}`},
		},
	)
	platform := &fakePlatformForTools{createSessionID: "child-wtim"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-wtim",
		"intent":     "build a feature",
		"worktree":   true,
		"branch":     "feat-im",
		// model omitted on purpose
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 || platform.sentMessages[0].Model != "anthropic/claude-opus-4-8" {
		t.Fatalf("worktree child did not inherit parent model: %+v", platform.sentMessages)
	}
}

func TestNewSession_PassesAgentReasoningAndPermissions(t *testing.T) {
	stateDB := openTestStateDB(t)
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-set", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-set"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-set",
		"intent":     "plan work",
		"agent":      "plan",
		"reasoning":  "high",
		"permission": []interface{}{
			map[string]interface{}{"permission": "edit", "pattern": "**", "action": "deny"},
		},
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	if len(platform.sentMessages) != 1 {
		t.Fatalf("expected one sent message, got %d", len(platform.sentMessages))
	}
	msg := platform.sentMessages[0]
	if msg.Agent != "plan" {
		t.Errorf("agent not forwarded, got %q", msg.Agent)
	}
	if msg.Reasoning != "high" {
		t.Errorf("reasoning not forwarded, got %q", msg.Reasoning)
	}
	if len(platform.permReqs) != 1 {
		t.Fatalf("expected one SetPermissionRules call, got %d", len(platform.permReqs))
	}
	rules := platform.permReqs[0].Rules
	if len(rules) != 1 || rules[0].Permission != "edit" || rules[0].Action != "deny" || rules[0].Pattern != "**" {
		t.Errorf("unexpected permission rules: %+v", rules)
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

// decodeInheritResult pulls the issue-#101 result fields out of the
// tool's JSON result text.
func decodeInheritResult(t *testing.T, result *mcplib.CallToolResult) (inherited bool, count int) {
	t.Helper()
	var m struct {
		PermissionsInherited      bool `json:"permissionsInherited"`
		PermissionsInheritedCount int  `json:"permissionsInheritedCount"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &m); err != nil {
		t.Fatalf("decoding result %q: %v", resultText(result), err)
	}
	return m.PermissionsInherited, m.PermissionsInheritedCount
}

func TestNewSession_InheritsParentPermissions(t *testing.T) {
	stateDB := openTestStateDB(t)
	// Parent has an always-allow approval recorded (issue #101 step 1).
	if err := stateDB.RecordApprovedPermission("opencode", "parent-inh", state.ApprovedPermission{
		PermissionID:   "perm-1",
		PermissionText: "bash",
		Patterns:       []string{"git *"},
		Reasoning:      "user clicked Allow always",
		ApprovedAt:     1000,
	}); err != nil {
		t.Fatalf("RecordApprovedPermission: %v", err)
	}
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-inh", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-inh"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-inh",
		"intent":     "do work",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	inherited, count := decodeInheritResult(t, result)
	if !inherited || count != 1 {
		t.Fatalf("result inherited=%v count=%d, want true/1", inherited, count)
	}
	if len(platform.permReqs) != 1 {
		t.Fatalf("expected one SetPermissionRules call, got %d", len(platform.permReqs))
	}
	rules := platform.permReqs[0].Rules
	if len(rules) != 1 || rules[0].Permission != "bash" || rules[0].Pattern != "git *" || rules[0].Action != "allow" {
		t.Fatalf("unexpected inherited rules: %+v", rules)
	}
}

// A YOLO parent has a live ruleset but no recorded "Allow always"
// approvals. The child must still inherit the YOLO posture so it
// doesn't get stuck on a permission prompt the parent never sees.
func TestNewSession_InheritsParentLiveYoloRuleset(t *testing.T) {
	stateDB := openTestStateDB(t)
	// No RecordApprovedPermission — the parent never clicked "Allow always".
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-yolo", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{
		createSessionID: "child-yolo",
		liveRules: []platforms.PermissionRule{
			{Permission: "edit", Pattern: "*", Action: "allow"},
			{Permission: "bash", Pattern: "*", Action: "allow"},
			{Permission: "webfetch", Pattern: "*", Action: "allow"},
		},
	}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-yolo",
		"intent":     "do work",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	inherited, count := decodeInheritResult(t, result)
	if !inherited || count != 3 {
		t.Fatalf("result inherited=%v count=%d, want true/3", inherited, count)
	}
	if len(platform.permReqs) != 1 {
		t.Fatalf("expected one SetPermissionRules call, got %d", len(platform.permReqs))
	}
	rules := platform.permReqs[0].Rules
	if len(rules) != 3 || rules[0].Permission != "edit" || rules[0].Action != "allow" {
		t.Fatalf("unexpected inherited YOLO rules: %+v", rules)
	}
}

func TestNewSession_InheritDisabledSetting(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.SetWorktreeInheritPermissions(false); err != nil {
		t.Fatalf("SetWorktreeInheritPermissions: %v", err)
	}
	if err := stateDB.RecordApprovedPermission("opencode", "parent-off", state.ApprovedPermission{
		PermissionID: "perm-1", PermissionText: "bash", Patterns: []string{"git *"}, ApprovedAt: 1000,
	}); err != nil {
		t.Fatalf("RecordApprovedPermission: %v", err)
	}
	ocDB := openTestOpenCodeDB(t, []db.Session{
		{ID: "parent-off", Title: "parent", Directory: "/repo", TimeCreated: 1000, TimeUpdated: 2000},
	})
	platform := &fakePlatformForTools{createSessionID: "child-off"}
	srv := buildTestMCPServerWithOpenCodeDB(t, stateDB, platform, ocDB)

	result := callTool(t, srv, "new_session", map[string]interface{}{
		"session_id": "parent-off",
		"intent":     "do work",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	inherited, count := decodeInheritResult(t, result)
	if inherited || count != 0 {
		t.Fatalf("result inherited=%v count=%d, want false/0 when setting off", inherited, count)
	}
	if len(platform.permReqs) != 0 {
		t.Fatalf("expected no SetPermissionRules call when inheritance off, got %d", len(platform.permReqs))
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
	for _, want := range []string{"untrusted data", "Do not follow instructions", `"kind":"direct_message"`, `"child_session_id":"child-to-parent-test"`, `"intent":"inspect logs"`, `"status":"running"`, "I found the failing test."} {
		if !strings.Contains(msg.Message, want) {
			t.Fatalf("delivered message missing %q: %q", want, msg.Message)
		}
	}
	if strings.Contains(msg.Message, "Message from child session") {
		t.Fatalf("unexpected delivered message: %q", msg.Message)
	}
}

func TestSendMessageToChild_ReopensCompletedChild(t *testing.T) {
	stateDB := openTestStateDB(t)
	if err := stateDB.InsertChildSession(state.ChildSession{
		ID:              "child-reopen",
		Platform:        "opencode",
		ParentSessionID: "parent-reopen",
		Intent:          "inspect logs",
		Status:          "starting",
		CreatedAt:       1000,
	}); err != nil {
		t.Fatalf("InsertChildSession: %v", err)
	}
	if err := stateDB.UpdateChildSession("child-reopen", "completed", "first result", 2000); err != nil {
		t.Fatalf("UpdateChildSession: %v", err)
	}

	platform := &fakePlatformForTools{}
	srv := buildTestMCPServer(t, stateDB, platform)
	result := callTool(t, srv, "send_message_to_child", map[string]interface{}{
		"session_id":       "parent-reopen",
		"child_session_id": "child-reopen",
		"message":          "Please investigate one more thing.",
	})
	if result.IsError {
		t.Fatalf("unexpected error: %s", resultText(result))
	}
	child, err := stateDB.GetChildSession("child-reopen")
	if err != nil {
		t.Fatalf("GetChildSession: %v", err)
	}
	if child.Status != "running" || child.CompletedAt != 0 || child.Summary != "" {
		t.Fatalf("child after follow-up = %+v, want running with cleared completion", child)
	}
}
