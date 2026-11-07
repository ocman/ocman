package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcptest"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/testutil"
)

type fakeSessionArchiver struct {
	fakeSessionService
	platform, id string
	archived     bool
	deferred     bool
	calls        int
	err          error
}

func (f *fakeSessionArchiver) SetSessionArchived(_ context.Context, platform, id string, archived bool) (internalmcp.ArchivedSession, error) {
	f.platform, f.id, f.archived = platform, id, archived
	f.calls++
	return internalmcp.ArchivedSession{Platform: platform, SessionID: id, Archived: archived && !f.deferred, Deferred: f.deferred}, f.err
}

func TestArchiveSessionTool(t *testing.T) {
	svc := &fakeSessionArchiver{}
	srv, err := mcptest.NewServer(t, internalmcp.ServerTools(internalmcp.Deps{SessionService: svc})...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, test := range []struct {
		action, platform string
		archived         bool
		want             string
	}{
		{"archive", "", true, `"archived": true`},
		{"archive", "opencode", true, `"archived": true`},
		{"unarchive", "r-machine:claudecode", false, `"archived": false`},
	} {
		args := map[string]any{"action": test.action, "session_id": " ses-1 "}
		if test.platform != "" {
			args["platform"] = test.platform
		}
		result := testutil.CallTool(t, srv, "sessions", args)
		text := testutil.ResultText(result)
		if result.IsError || svc.platform != test.platform || svc.id != "ses-1" || svc.archived != test.archived || !strings.Contains(text, test.want) || strings.Contains(text, "deferred") {
			t.Fatalf("%s: result = %q, service = %#v", test.action, text, svc)
		}
	}

	svc.deferred = true
	result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "archive", "session_id": "ses-1"})
	if text := testutil.ResultText(result); result.IsError || !strings.Contains(text, `"deferred": true`) || !strings.Contains(text, `"archived": false`) {
		t.Fatalf("deferred result = %q", text)
	}
	svc.deferred = false

	for _, test := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "session_id is required"},
		{map[string]any{"session_id": " "}, "session_id is required"},
		{map[string]any{"session_id": 42}, "session_id is required"},
		{map[string]any{"session_id": "x", "platform": true}, "platform must be a string"},
	} {
		for _, action := range []string{"archive", "unarchive"} {
			test.args["action"] = action
			before := svc.calls
			result := testutil.CallTool(t, srv, "sessions", test.args)
			if !result.IsError || testutil.ResultText(result) != test.want || svc.calls != before {
				t.Fatalf("args %#v: result = %q, calls = %d", test.args, testutil.ResultText(result), svc.calls)
			}
		}
	}
	for _, test := range []struct {
		err  error
		want string
	}{
		{platforms.ErrNotFound, "session not found"},
		{internalmcp.AmbiguousSessionError{Platforms: []string{"a", "b"}}, "session_id exists on multiple platforms (a, b); pass platform"},
		{internalmcp.ErrArchiveBusyRemote, internalmcp.ErrArchiveBusyRemote.Error()},
		{errors.New("private detail"), "session request failed"},
	} {
		svc.err = test.err
		result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": "archive", "session_id": "x"})
		if !result.IsError || testutil.ResultText(result) != test.want {
			t.Fatalf("error %v: result = %q", test.err, testutil.ResultText(result))
		}
	}
}

func TestArchiveSessionHelpDocumentsBusySessions(t *testing.T) {
	srv := sessionServer(t, &fakeSessionService{})
	help := testutil.ResultText(testutil.CallTool(t, srv, "sessions", map[string]any{"action": "help"}))
	for _, text := range []string{`"archive"`, `"unarchive"`, "when its current turn finishes", "deferred", "archiving itself", "rejected"} {
		if !strings.Contains(help, text) {
			t.Fatalf("help missing %q: %s", text, help)
		}
	}
}

func TestArchiveSessionUnavailableWithoutArchiver(t *testing.T) {
	srv := sessionServer(t, &fakeSessionService{})
	for _, action := range []string{"archive", "unarchive"} {
		result := testutil.CallTool(t, srv, "sessions", map[string]any{"action": action, "session_id": "x"})
		if !result.IsError || testutil.ResultText(result) != "session archiving is unavailable" {
			t.Fatalf("%s: result = %q", action, testutil.ResultText(result))
		}
	}
}
