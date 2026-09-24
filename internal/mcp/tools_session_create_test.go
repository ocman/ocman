package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeSessionCreator struct {
	fakeSessionService
	got     internalmcp.CreateSessionRequest
	created internalmcp.CreatedSession
	err     error
}

func (f *fakeSessionCreator) CreateSession(_ context.Context, req internalmcp.CreateSessionRequest) (internalmcp.CreatedSession, error) {
	f.got = req
	return f.created, f.err
}

func TestCreateSessionTool(t *testing.T) {
	svc := &fakeSessionCreator{created: internalmcp.CreatedSession{Platform: "opencode", SessionID: "ses-new", Directory: "/repo"}}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)

	result := callTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": " fix it ", "model": "anthropic/claude", "agent": "plan", "platform": "opencode", "session_id": "ses-caller"})
	want := internalmcp.CreateSessionRequest{Prompt: "fix it", Model: "anthropic/claude", Agent: "plan", Platform: "opencode", SessionID: "ses-caller"}
	if result.IsError || svc.got != want || !strings.Contains(resultText(result), `"session_id": "ses-new"`) {
		t.Fatalf("result = %q, request = %#v", resultText(result), svc.got)
	}

	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{"directory": "/repo"}, want: "prompt is required"},
		{args: map[string]any{"prompt": "x", "directory": "/repo", "model": "claude"}, want: "model must be provider/model"},
		{args: map[string]any{"prompt": "x", "directory": "repo"}, want: "directory must be absolute"},
		{args: map[string]any{"prompt": "x", "session_id": "ses-1"}, want: "platform and session_id must be provided together"},
		{args: map[string]any{"prompt": "x"}, want: "directory or the calling session's platform and session_id is required"},
	} {
		if result := callTool(t, srv, "sessions", withCreate(test.args)); !result.IsError || resultText(result) != test.want {
			t.Fatalf("args %#v: result = %q", test.args, resultText(result))
		}
	}

	for _, test := range []struct {
		err     error
		created internalmcp.CreatedSession
		want    string
	}{
		{err: fmt.Errorf("%w: unknown platform", internalmcp.ErrInvalidSessionCreate), want: "invalid session request: unknown platform"},
		{err: platforms.ErrNotFound, want: "session not found"},
		{err: errors.New("internal detail"), want: "session request failed"},
		{err: errors.New("send"), created: internalmcp.CreatedSession{Platform: "opencode", SessionID: "ses-half"}, want: "session ses-half was created on opencode but the prompt failed"},
	} {
		svc.err, svc.created = test.err, test.created
		if result := callTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "x", "directory": "/repo"}); !result.IsError || resultText(result) != test.want {
			t.Fatalf("err %v: result = %q", test.err, resultText(result))
		}
	}
}

func withCreate(args map[string]any) map[string]any {
	out := map[string]any{"action": "create"}
	for k, v := range args {
		out[k] = v
	}
	return out
}

func TestCreateSessionUnavailableWithoutCreator(t *testing.T) {
	srv := sessionServer(t, &fakeSessionService{})
	if result := callTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "x", "directory": "/repo"}); !result.IsError || resultText(result) != "session creation is unavailable" {
		t.Fatalf("result = %q", resultText(result))
	}
}
