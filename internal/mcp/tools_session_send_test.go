package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/testutil"
	"github.com/mark3labs/mcp-go/mcptest"
)

type fakeSessionSender struct {
	fakeSessionService
	platform, id, prompt string
	calls                int
	sendErr              error
}

func (f *fakeSessionSender) SendSessionMessage(_ context.Context, platform, id, prompt string) (internalmcp.SentSession, error) {
	f.platform, f.id, f.prompt = platform, id, prompt
	f.calls++
	return internalmcp.SentSession{Platform: platform, SessionID: id, Sent: true}, f.sendErr
}

func TestSendSessionTool(t *testing.T) {
	svc := &fakeSessionSender{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, platform := range []string{"", "opencode", "r-machine:claudecode"} {
		args := map[string]any{"action": "send", "session_id": " ses-recipient ", "prompt": "  Keep this formatting.\n"}
		if platform != "" {
			args["platform"] = platform
		}
		result := testutil.CallTool(t, srv, "sessions", args)
		if result.IsError || svc.platform != platform || svc.id != "ses-recipient" || svc.prompt != args["prompt"] || !strings.Contains(testutil.ResultText(result), `"sent": true`) {
			t.Fatalf("result = %q, service = %#v", testutil.ResultText(result), svc)
		}
	}
	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"prompt": "go"}, "session_id is required"},
		{map[string]any{"session_id": "x"}, "prompt is required"},
		{map[string]any{"session_id": " ", "prompt": "go"}, "session_id is required"},
		{map[string]any{"session_id": 42, "prompt": "go"}, "session_id is required"},
		{map[string]any{"session_id": "x", "prompt": "\n "}, "prompt is required"},
		{map[string]any{"session_id": "x", "prompt": true}, "prompt is required"},
		{map[string]any{"session_id": "x", "prompt": "go", "platform": true}, "platform must be a string"},
	} {
		test.args["action"] = "send"
		before := svc.calls
		result := testutil.CallTool(t, srv, "sessions", test.args)
		if !result.IsError || testutil.ResultText(result) != test.want || svc.calls != before {
			t.Fatalf("args %#v: result = %q, calls = %d", test.args, testutil.ResultText(result), svc.calls)
		}
	}
	for _, test := range []struct {
		err  error
		want string
	}{
		{platforms.ErrNotFound, "session not found"},
		{internalmcp.AmbiguousSessionError{Platforms: []string{"a", "b"}}, "session_id exists on multiple platforms (a, b); pass platform"},
		{errors.New("private detail"), "session request failed"},
	} {
		svc.sendErr = test.err
		result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "send", "session_id": "x", "prompt": "go"})
		if !result.IsError || testutil.ResultText(result) != test.want {
			t.Fatalf("error %v: result = %q", test.err, testutil.ResultText(result))
		}
	}
}

func TestSendSessionUnavailableWithoutSender(t *testing.T) {
	srv := sessionServer(t, &fakeSessionService{})
	result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "send", "session_id": "x", "prompt": "go"})
	if !result.IsError || testutil.ResultText(result) != "session messaging is unavailable" {
		t.Fatalf("result = %q", testutil.ResultText(result))
	}
}
