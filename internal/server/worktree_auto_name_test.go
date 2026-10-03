package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type autoWorktreeOwner struct {
	hostsvc.Host
	request hostsvc.WorktreeSessionRequest
}

func (h *autoWorktreeOwner) RemoteID() string { return "machine" }

func (h *autoWorktreeOwner) CreateWorktreeSession(_ context.Context, request hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.request = request
	return &hostsvc.WorktreeSessionResult{SessionID: "child", Branch: "fix-login-1234", WorktreePath: "/remote/worktree"}, nil
}

func TestWorktreeAutomaticNameRoutesPromptToOwner(t *testing.T) {
	owner := &autoWorktreeOwner{}
	srv := &Server{hostRouter: hostsvc.NewRouter(nil)}
	srv.hostRouter.RegisterRemote("machine", owner)
	r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(`{"projectDir":"/remote/repo","remoteId":"machine","autoName":true,"prompt":"Fix login"}`))
	w := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !owner.request.AutoName || owner.request.Prompt != "Fix login" || owner.request.ProjectDir != "/remote/repo" {
		t.Fatalf("wrong owner request: %+v", owner.request)
	}
	if !strings.Contains(w.Body.String(), `"branch":"fix-login-1234"`) {
		t.Fatalf("missing generated name: %s", w.Body.String())
	}
}

func TestWorktreeAutomaticNameRejectsDisconnectedOwner(t *testing.T) {
	srv := &Server{hostRouter: hostsvc.NewRouter(nil)}
	r := httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(`{"projectDir":"/remote/repo","remoteId":"gone","autoName":true,"prompt":"Fix login"}`))
	w := httptest.NewRecorder()
	srv.handleWorktreeCreateAndLaunch(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
}

type selectedWorkspaceOwner struct {
	hostsvc.Host
	calls int
}

func (h *selectedWorkspaceOwner) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	h.calls++
	return []git.Worktree{{Path: "/repo/feature", Branch: "feature"}}, nil
}

func TestWorktreeListResolvesSelectedWorkspaceOnExplicitOwner(t *testing.T) {
	local, remote := &selectedWorkspaceOwner{}, &selectedWorkspaceOwner{}
	srv := &Server{hostRouter: hostsvc.NewRouter(local)}
	srv.hostRouter.RegisterRemote("machine", remote)
	for _, tc := range []struct {
		owner  string
		status int
	}{
		{"machine", http.StatusOK},
		{"gone", http.StatusServiceUnavailable},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/api/worktree/list?dir=/repo/feature&remoteId="+tc.owner, nil)
		srv.handleWorktreeList(w, r)
		if w.Code != tc.status {
			t.Fatalf("owner %s: status %d, want %d: %s", tc.owner, w.Code, tc.status, w.Body.String())
		}
	}
	if local.calls != 0 || remote.calls != 1 {
		t.Fatalf("wrong owner called: local=%d remote=%d", local.calls, remote.calls)
	}
}

// The first message rides on create-and-launch and is delivered to the
// owner's session server-side; a send failure is reported, not fatal.
func TestWorktreeCreateAndLaunchSendsFirstMessage(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "sent", true: "failed"}[fail], func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			srv.hostRouter = hostsvc.NewRouter(nil)
			srv.hostRouter.RegisterRemote("machine", &autoWorktreeOwner{})
			var got platforms.SendMessageRequest
			reg.Register(&fakePlatform{
				id:       "r-machine:opencode",
				sessions: []db.Session{mkSession("r-machine:opencode", "child", "t", 1)},
				sendMessageFn: func(req platforms.SendMessageRequest) error {
					got = req
					if fail {
						return errors.New("boom")
					}
					return nil
				},
			})
			body := `{"projectDir":"/remote/repo","remoteId":"machine","autoName":true,"prompt":"Fix login",` +
				`"send":{"message":"Fix login","agent":"build","images":[{"url":"data:x","mime":"image/png"}]}}`
			w := httptest.NewRecorder()
			srv.handleWorktreeCreateAndLaunch(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			if got.SessionID != "child" || got.Message != "Fix login" || got.Agent != "build" || len(got.Images) != 1 {
				t.Fatalf("send = %+v", got)
			}
			var resp struct {
				Sent bool   `json:"firstMessageSent"`
				Err  string `json:"firstMessageError"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatal(err)
			}
			if resp.Sent == fail || (resp.Err != "") != fail {
				t.Fatalf("response = %+v", resp)
			}
		})
	}
}
