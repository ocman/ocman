package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeSessionService struct {
	sessions []db.Session
	detail   *platforms.SessionDetail
	dir      string
	platform string
	id       string
	limit    int
	err      error
}

func (f *fakeSessionService) ListSessions(_ context.Context, directory string) ([]db.Session, error) {
	f.dir = directory
	return f.sessions, f.err
}

func (f *fakeSessionService) GetSession(_ context.Context, platform, id string, limit int) (*platforms.SessionDetail, error) {
	f.platform, f.id, f.limit = platform, id, limit
	return f.detail, f.err
}

func sessionServer(t *testing.T, svc *fakeSessionService) *mcptest.Server {
	t.Helper()
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	return srv
}

func TestSessionToolReadActions(t *testing.T) {
	detail := &platforms.SessionDetail{Session: &db.Session{ID: "ses-review", Title: "Review API", Directory: "/repo"}}
	svc := &fakeSessionService{sessions: []db.Session{
		{ID: "ses-review", Title: "Review API", Directory: "/repo", Platform: "opencode"},
		{ID: "ses-build", Title: "Build UI", Directory: "/other", Platform: "opencode"},
	}, detail: detail}
	srv := sessionServer(t, svc)

	help := callTool(t, srv, "sessions", map[string]any{"action": "help"})
	for _, text := range []string{"list", "search", "get", "read-only", "message_limit", "output_schema"} {
		if !strings.Contains(resultText(help), text) {
			t.Fatalf("help result missing %q: %s", text, resultText(help))
		}
	}

	listed := callTool(t, srv, "sessions", map[string]any{"action": "list", "directory": "/repo", "limit": 1})
	if listed.IsError || svc.dir != "/repo" || !strings.Contains(resultText(listed), "ses-review") || strings.Contains(resultText(listed), "ses-build") {
		t.Fatalf("list result = %q, directory = %q", resultText(listed), svc.dir)
	}

	searched := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "build"})
	if searched.IsError || strings.Contains(resultText(searched), "ses-review") || !strings.Contains(resultText(searched), "ses-build") {
		t.Fatalf("search result = %q", resultText(searched))
	}

	got := callTool(t, srv, "sessions", map[string]any{"action": "get", "session_id": "ses-review", "platform": "opencode", "message_limit": 10})
	if got.IsError || svc.id != "ses-review" || svc.platform != "opencode" || svc.limit != 10 || !strings.Contains(resultText(got), "Review API") {
		t.Fatalf("get result = %q, service = %#v", resultText(got), svc)
	}
}

func TestSessionToolValidationAndErrors(t *testing.T) {
	svc := &fakeSessionService{}
	srv := sessionServer(t, svc)
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{}, want: "action is required"},
		{args: map[string]any{"action": "unknown"}, want: "unknown action"},
		{args: map[string]any{"action": "search"}, want: "query is required"},
		{args: map[string]any{"action": "search", "query": "  "}, want: "query is required"},
		{args: map[string]any{"action": "list", "limit": 0}, want: "limit must be between 1 and 500"},
		{args: map[string]any{"action": "get", "session_id": "x"}, want: "platform is required"},
		{args: map[string]any{"action": "get", "platform": "opencode", "session_id": "x", "message_limit": 101}, want: "message_limit must be between 0 and 100"},
	} {
		result := callTool(t, srv, "sessions", test.args)
		if !result.IsError || resultText(result) != test.want {
			t.Fatalf("args %#v: result = %q", test.args, resultText(result))
		}
	}

	svc.err = platforms.ErrNotFound
	if result := callTool(t, srv, "sessions", map[string]any{"action": "get", "platform": "opencode", "session_id": "missing"}); !result.IsError || resultText(result) != "session not found" {
		t.Fatalf("not found result = %q", resultText(result))
	}
	svc.err = errors.New("database details")
	if result := callTool(t, srv, "sessions", map[string]any{"action": "list"}); !result.IsError || resultText(result) != "session request failed" {
		t.Fatalf("internal error result = %q", resultText(result))
	}
}
