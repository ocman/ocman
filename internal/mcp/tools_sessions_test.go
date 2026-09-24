package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

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
	matches  []db.TextMatch
	query    string
	since    int64
	textErr  error
}

func (f *fakeSessionService) SearchSessionText(_ context.Context, query, directory string, since int64) ([]db.TextMatch, error) {
	f.query, f.dir, f.since = query, directory, since
	return f.matches, f.textErr
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
	for _, text := range []string{"list", "search", "get", "create", "provider/model", "message_limit", "output_schema"} {
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

func TestSessionToolContentSearch(t *testing.T) {
	hit := func(platform, session, part string) db.TextMatch {
		return db.TextMatch{Platform: platform, SessionID: session, PartID: part, Role: "user", Snippet: "run weave-cli now"}
	}
	svc := &fakeSessionService{sessions: []db.Session{
		{ID: "ses-title", Title: "weave-cli rollout", Platform: "opencode"},
		{ID: "ses-body", Title: "Deploy", Platform: "opencode"},
		{ID: "ses-body", Title: "Same ID on a remote", Platform: "r-x:opencode"},
		{ID: "ses-none", Title: "Unrelated", Platform: "opencode"},
	}, matches: []db.TextMatch{
		hit("opencode", "ses-body", "p1"), hit("opencode", "ses-body", "p2"), hit("opencode", "ses-body", "p3"),
		hit("opencode", "ses-body", "p4"), hit("opencode", "ses-body", "p5"), hit("opencode", "ses-body", "p6"),
		hit("opencode", "ses-gone", "p7"),
	}}
	srv := sessionServer(t, svc)

	before := time.Now().AddDate(0, 0, -3).UnixMilli()
	result := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "weave-cli", "content": true, "since_days": 3, "directory": "/repo"})
	if result.IsError || svc.query != "weave-cli" || svc.dir != "/repo" || svc.since < before-1000 || svc.since > before+1000 {
		t.Fatalf("result = %q, service = %#v", resultText(result), svc)
	}
	var got []struct {
		ID       string `json:"id"`
		Platform string `json:"platform"`
		Matches  []struct {
			PartID  string `json:"partId"`
			Snippet string `json:"snippet"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(resultText(result)), &got); err != nil {
		t.Fatal(err)
	}
	// Title hit without content, content hit capped at 5; the remote twin,
	// the unrelated session, and a hit outside the listing are dropped.
	if len(got) != 2 || got[0].ID != "ses-title" || len(got[0].Matches) != 0 ||
		got[1].ID != "ses-body" || got[1].Platform != "opencode" || len(got[1].Matches) != 5 || got[1].Matches[0].Snippet != "run weave-cli now" {
		t.Fatalf("results = %+v", got)
	}

	if limited := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "weave-cli", "content": true, "limit": 1}); strings.Contains(resultText(limited), "ses-body") {
		t.Fatalf("limit not applied: %q", resultText(limited))
	}

	svc.query = ""
	if plain := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "weave-cli"}); plain.IsError || svc.query != "" || strings.Contains(resultText(plain), "ses-body") {
		t.Fatalf("content search ran without opt-in: %q", resultText(plain))
	}

	for _, days := range []int{0, 366} {
		if bad := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "x", "content": true, "since_days": days}); !bad.IsError || resultText(bad) != "since_days must be between 1 and 365" {
			t.Fatalf("since_days %d: %q", days, resultText(bad))
		}
	}
}

func TestSessionToolContentSearchError(t *testing.T) {
	svc := &fakeSessionService{}
	srv := sessionServer(t, svc)
	svc.textErr = errors.New("disk")
	if result := callTool(t, srv, "sessions", map[string]any{"action": "search", "query": "x", "content": true}); !result.IsError || resultText(result) != "session request failed" || svc.query != "x" {
		t.Fatalf("result = %q", resultText(result))
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
		{args: map[string]any{"action": "get"}, want: "session_id is required"},
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
	svc.err = internalmcp.AmbiguousSessionError{Platforms: []string{"opencode", "r-box:opencode"}}
	if result := callTool(t, srv, "sessions", map[string]any{"action": "get", "session_id": "dup"}); !result.IsError || resultText(result) != "session_id exists on multiple platforms (opencode, r-box:opencode); pass platform" {
		t.Fatalf("ambiguous result = %q", resultText(result))
	}
	svc.err = errors.New("database details")
	if result := callTool(t, srv, "sessions", map[string]any{"action": "list"}); !result.IsError || resultText(result) != "session request failed" {
		t.Fatalf("internal error result = %q", resultText(result))
	}
}
