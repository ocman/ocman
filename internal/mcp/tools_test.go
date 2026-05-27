package mcp_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	_ "modernc.org/sqlite"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/worktree"
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

func (fakePlatformBase) ID() platforms.ID                  { return "fake" }
func (fakePlatformBase) DisplayName() string               { return "Fake" }
func (fakePlatformBase) Available(_ context.Context) bool  { return true }
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
	t.Helper()

	reg := platforms.NewRegistry()
	reg.Register(platform)

	fakeWT := internalmcp.WorktreeCreator(func(_ context.Context, req worktree.CreateRequest) (*worktree.CreateResult, error) {
		return &worktree.CreateResult{
			Path:   "/tmp/worktrees/repo/" + req.Branch,
			Branch: req.Branch,
		}, nil
	})
	fakeTmux := internalmcp.TmuxLauncher(func(_, _ string) (string, bool, error) {
		return "~/src/repo:wt-branch", true, nil
	})
	fakePort := internalmcp.PortDiscoverer(func(_ string) string { return "12345" })

	deps := internalmcp.Deps{
		StateDB:        stateDB,
		Registry:       reg,
		PlatformID:     "opencode",
		CreateWorktree: fakeWT,
		LaunchTmux:     fakeTmux,
		DiscoverPort:   fakePort,
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
