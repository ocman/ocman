package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/testutil"
)

type fakeSessionCreator struct {
	fakeSessionService
	calls   int
	got     internalmcp.CreateSessionRequest
	created internalmcp.CreatedSession
	err     error
}

func (f *fakeSessionCreator) CreateSession(_ context.Context, req internalmcp.CreateSessionRequest) (internalmcp.CreatedSession, error) {
	f.calls++
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

	result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": " fix it ", "model": "anthropic/claude", "agent": "plan", "platform": "opencode", "session_id": "ses-caller"})
	want := internalmcp.CreateSessionRequest{Prompt: "fix it", Model: "anthropic/claude", Agent: "plan", Platform: "opencode", SessionID: "ses-caller"}
	if result.IsError || svc.got != want || !strings.Contains(testutil.ResultText(result), `"session_id": "ses-new"`) {
		t.Fatalf("result = %q, request = %#v", testutil.ResultText(result), svc.got)
	}
	for _, value := range []bool{false, true} {
		result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "go", "directory": "/repo", "platform": "opencode", "model": "p/m", "worktree": value})
		if result.IsError || svc.got.Worktree == nil || *svc.got.Worktree != value {
			t.Fatalf("worktree override = %#v, %q", svc.got, testutil.ResultText(result))
		}
	}

	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{args: map[string]any{"directory": "/repo"}, want: "prompt is required"},
		{args: map[string]any{"prompt": "x", "directory": "/repo", "model": "claude"}, want: "model must be provider/model"},
		{args: map[string]any{"prompt": "x", "directory": "repo"}, want: "directory must be absolute"},
		{args: map[string]any{"prompt": "x", "session_id": "ses-1"}, want: "session_id requires platform"},
		{args: map[string]any{"prompt": "x", "platform": "claudecode"}, want: "directory or the calling session's platform and session_id is required"},
		{args: map[string]any{"prompt": "x"}, want: "directory or the calling session's platform and session_id is required"},
		{args: map[string]any{"prompt": "x", "directory": "/repo", "worktree": "false"}, want: "worktree must be a boolean"},
		{args: map[string]any{"prompt": "x", "directory": "/repo", "model": "opencode:p/m"}, want: "create requires caller platform and session_id, or explicit platform, model, and directory"},
		{args: map[string]any{"prompt": "x", "directory": "/repo", "platform": "opencode", "session_id": " ", "model": " "}, want: "create requires caller platform and session_id, or explicit platform, model, and directory"},
	} {
		if result := testutil.CallTool(t, srv, "sessions", withCreate(test.args)); !result.IsError || testutil.ResultText(result) != test.want {
			t.Fatalf("args %#v: result = %q", test.args, testutil.ResultText(result))
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
		if result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "x", "directory": "/repo", "platform": "opencode", "model": "p/m"}); !result.IsError || testutil.ResultText(result) != test.want {
			t.Fatalf("err %v: result = %q", test.err, testutil.ResultText(result))
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
	if result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "x", "directory": "/repo"}); !result.IsError || testutil.ResultText(result) != "session creation is unavailable" {
		t.Fatalf("result = %q", testutil.ResultText(result))
	}
}

func TestCreateSessionToolExplicitPlatformWithoutCaller(t *testing.T) {
	for _, platform := range []string{"opencode", "claudecode", "r-machine:claudecode", "other-platform"} {
		t.Run(platform, func(t *testing.T) {
			svc := &fakeSessionCreator{created: internalmcp.CreatedSession{Platform: platform, SessionID: "new", Directory: "/repo"}}
			srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "go", "directory": "/repo", "platform": platform, "model": "p/m"})
			if result.IsError || svc.got.Platform != platform || svc.got.SessionID != "" {
				t.Fatalf("create on %s = %q, request = %#v", platform, testutil.ResultText(result), svc.got)
			}
		})
	}
}

func TestCreateSessionRequiresCallerOrExplicitTarget(t *testing.T) {
	for mask := 0; mask < 8; mask++ {
		t.Run(fmt.Sprintf("target-fields-%d", mask), func(t *testing.T) {
			svc := &fakeSessionCreator{}
			srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(srv.Close)
			args := map[string]any{"action": "create", "prompt": "go"}
			for i, field := range []struct{ key, value string }{{"directory", "/repo"}, {"platform", "opencode"}, {"model", "p/m"}} {
				if mask&(1<<i) != 0 {
					args[field.key] = field.value
				}
			}
			result := testutil.CallTool(t, srv, "sessions", args)
			if mask == 7 {
				if result.IsError || svc.calls != 1 {
					t.Fatalf("explicit target rejected: %s", testutil.ResultText(result))
				}
			} else if !result.IsError || svc.calls != 0 {
				t.Fatalf("ambiguous create reached service: calls=%d, result=%s", svc.calls, testutil.ResultText(result))
			}
			if mask != 7 && mask&1 != 0 && testutil.ResultText(result) != "create requires caller platform and session_id, or explicit platform, model, and directory" {
				t.Fatalf("unhelpful validation error: %s", testutil.ResultText(result))
			}
			// Caller context permits model/directory omission and explicit overrides.
			args["platform"], args["session_id"] = "opencode", "caller"
			before := svc.calls
			result = testutil.CallTool(t, srv, "sessions", args)
			if result.IsError || svc.calls != before+1 || svc.got.SessionID != "caller" {
				t.Fatalf("caller create rejected: %s", testutil.ResultText(result))
			}
		})
	}
}
