package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type autoWorktreeOwner struct {
	hostsvc.Host
	request hostsvc.WorktreeSessionRequest
}

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
