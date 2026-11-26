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
		{err: errors.New("internal detail"), want: "session creation failed during setup: unexpected server error. Retrying unchanged is unlikely to help; report this message to the user. Ocman logged the cause as \"MCP session creation failed\" with this stage."},
		{err: errors.New("send"), created: internalmcp.CreatedSession{Platform: "opencode", SessionID: "ses-half"}, want: "session ses-half was created on opencode but setup or prompt delivery failed: unexpected server error. Prompt delivery is not confirmed; history may be empty. Do not create another session or automatically resend the prompt; inspect this session and ask the user before retrying.\n/session/ses-half?platform=opencode"},
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

func TestCreateSessionToolExplainsFailures(t *testing.T) {
	svc := &fakeSessionCreator{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, test := range []struct {
		err  error
		want []string
	}{
		{&internalmcp.SessionCreateStageError{Stage: "worktree setup", Err: errors.New("private git output")}, []string{"during worktree setup", "unexpected server error", "unlikely to help", "MCP session creation failed"}},
		{&internalmcp.SessionCreateStageError{Stage: "runtime preparation", Err: platforms.ErrPlatformUnreachable}, []string{"during runtime preparation", "no running, ready instance", "Retrying in a minute may succeed"}},
		{&internalmcp.SessionCreateStageError{Stage: "target validation", Err: platforms.ErrRemoteOffline}, []string{"machine is not connected", "Retry after it reconnects"}},
		{&internalmcp.SessionCreateStageError{Stage: "first prompt", Err: platforms.ErrPlatformUnauthenticated}, []string{"rejected ocman's credentials", "Do not retry"}},
		{&internalmcp.SessionCreateStageError{Stage: "session allocation", Err: &platforms.BusyError{Message: "busy"}}, []string{"target is busy", "Retry shortly"}},
		{&internalmcp.SessionCreateStageError{Stage: "session allocation", Err: context.DeadlineExceeded}, []string{"timed out", "sessions list"}},
		{&internalmcp.SessionCreateStageError{Stage: "session allocation", Err: context.Canceled}, []string{"cancelled"}},
		{&internalmcp.SessionCreateStageError{Stage: "session allocation", Err: platforms.ErrUnsupported}, []string{"does not support", "Do not retry unchanged"}},
		{&internalmcp.SessionCreateStageError{Stage: "session allocation", Err: &platforms.CreationPendingError{SessionID: "ocm_pending", Err: context.Canceled}}, []string{"outcome is uncertain after session allocation", "ocm_pending", "Do not create another session"}},
		{&internalmcp.SessionCreateStageError{Stage: "worktree setup", Err: &platforms.CreationPendingError{SessionID: "ocm_pending", Err: platforms.ErrPlatformUnreachable}}, []string{"outcome is uncertain", "Do not create another session"}},
	} {
		svc.err = test.err
		result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "x", "directory": "/repo", "platform": "opencode", "model": "p/m"})
		text := testutil.ResultText(result)
		if !result.IsError || strings.Contains(text, "private") {
			t.Fatalf("err %v: result = %q", test.err, text)
		}
		for _, want := range test.want {
			if !strings.Contains(text, want) {
				t.Errorf("err %v: %q lacks %q", test.err, text, want)
			}
		}
	}
}

func TestSessionCreateStageErrorWrapsCause(t *testing.T) {
	err := &internalmcp.SessionCreateStageError{Stage: "worktree setup", Err: platforms.ErrBusy}
	if err.Error() != "worktree setup: "+platforms.ErrBusy.Error() || !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("stage error = %q", err)
	}
}

func TestCreateSessionToolExplainsPartialFailures(t *testing.T) {
	svc := &fakeSessionCreator{created: internalmcp.CreatedSession{Platform: "r-machine:opencode", SessionID: "ses-half"}}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, test := range []struct {
		stage string
		err   error
		cause string
	}{
		{"first prompt", context.DeadlineExceeded, "request timed out"},
		{"first prompt", context.Canceled, "request was cancelled"},
		{"first prompt", platforms.ErrPlatformUnauthenticated, "rejected ocman's credentials"},
		{"creator recording", errors.New("private database detail"), "unexpected server error"},
	} {
		t.Run(test.stage+"/"+test.cause, func(t *testing.T) {
			svc.err = &internalmcp.SessionCreateStageError{Stage: test.stage, Err: test.err}
			result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "create", "prompt": "private prompt", "directory": "/repo", "platform": "opencode", "model": "p/m"})
			text := testutil.ResultText(result)
			if !result.IsError || strings.Contains(text, "private") {
				t.Fatalf("partial failure leaked input or reported success: %q", text)
			}
			for _, want := range []string{"ses-half", "during " + test.stage, test.cause, "history may be empty", "Do not create another session", "ask the user before retrying", "/session/ses-half?platform=r-machine%3Aopencode"} {
				if !strings.Contains(text, want) {
					t.Errorf("partial failure %q lacks %q", text, want)
				}
			}
		})
	}
}
