package gitmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/git"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/module/moduletest"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// fakeHost answers the git and worktree calls; anything else panics.
type fakeHost struct {
	hostsvc.Host
	err      error
	calls    []string
	checkout string
	created  hostsvc.WorktreeSessionRequest
	baseRef  string
}

func (h *fakeHost) RemoteID() string { return "rem1" }

func (h *fakeHost) GitInfo(_ context.Context, dirs []string) (map[string]git.Info, error) {
	h.calls = append(h.calls, "info:"+strings.Join(dirs, "|"))
	return map[string]git.Info{dirs[0]: {Branch: "main"}}, h.err
}

func (h *fakeHost) GitBranches(context.Context, string) ([]string, error) {
	h.calls = append(h.calls, "branches")
	return nil, h.err
}

func (h *fakeHost) GitCheckout(_ context.Context, _, branch string) error {
	h.calls = append(h.calls, "checkout")
	h.checkout = branch
	return h.err
}

func (h *fakeHost) GitDiff(context.Context, string, hostsvc.GitDiffOptions) (*git.Diff, error) {
	h.calls = append(h.calls, "diff")
	return &git.Diff{}, h.err
}

func (h *fakeHost) ListWorktrees(context.Context, string) ([]git.Worktree, error) {
	h.calls = append(h.calls, "list")
	return nil, h.err
}

func (h *fakeHost) WorktreeDefaultBaseRef(context.Context, string) (string, error) {
	h.calls = append(h.calls, "base")
	return h.baseRef, h.err
}

func (h *fakeHost) RemoveWorktree(context.Context, hostsvc.RemoveWorktreeRequest) error {
	h.calls = append(h.calls, "remove")
	return h.err
}

func (h *fakeHost) CreateWorktreeSession(_ context.Context, req hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.calls = append(h.calls, "create")
	h.created = req
	if h.err != nil {
		return nil, h.err
	}
	return &hostsvc.WorktreeSessionResult{SessionID: "child", WorktreePath: "/r/.wt/x", Branch: "x"}, nil
}

// harness wires a module to one fake host. owned is the remoteId the fake
// Owner accepts; any other non-empty id fails closed like the server.
type harness struct {
	host   *fakeHost
	steps  []string
	sent   []platforms.SendMessageRequest
	sendTo string
	m      *Module
	guards module.Guards
}

func newHarness(guards module.Guards) *harness {
	h := &harness{host: &fakeHost{}, guards: guards}
	h.m = NewModule(Deps{
		Owner: func(w http.ResponseWriter, _, remoteID string) (hostsvc.Host, bool) {
			if remoteID != "" && remoteID != "local" && remoteID != "rem1" {
				http.Error(w, "remote_not_connected", http.StatusServiceUnavailable)
				return nil, false
			}
			return h.host, true
		},
		ForDir: func(string) hostsvc.Host { return h.host },
		ResolvePlatform: func(*http.Request, hostsvc.Host, string, string, string) (string, string, error) {
			return "r-rem1:opencode", "opencode", nil
		},
		InheritPermissions: func(_ *http.Request, _, parent string) func(string) (int, string) {
			h.steps = append(h.steps, "build:"+parent)
			return func(child string) (int, string) {
				h.steps = append(h.steps, "apply:"+child)
				return 2, ""
			}
		},
		SendNow: func(_ context.Context, platform string, req platforms.SendMessageRequest) error {
			h.steps = append(h.steps, "send")
			h.sendTo = platform
			h.sent = append(h.sent, req)
			return nil
		},
	})
	return h
}

func pass(h http.HandlerFunc) http.HandlerFunc { return h }

func (h *harness) serve(method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.m.Routes(moduletest.Router{Mux: mux, Auth: h.guards.Auth, Localhost: h.guards.Localhost})
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(method, target, strings.NewReader(body)))
	return rr
}

// Reads need a login, mutations need localhost, and a wrong method is 405
// before either guard runs.
func TestRoutesGuardsAndMethods(t *testing.T) {
	deny := func(code int) func(http.HandlerFunc) http.HandlerFunc {
		return func(http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
		}
	}
	h := newHarness(module.Guards{Auth: deny(http.StatusUnauthorized), Localhost: deny(http.StatusForbidden)})
	for _, tc := range []struct {
		path, method string
		guard        int
	}{
		{"/api/git/diff?dir=/r", http.MethodGet, http.StatusUnauthorized},
		{"/api/git/info?dirs=/r&remoteId=local", http.MethodGet, http.StatusUnauthorized},
		{"/api/git/branches?dir=/r", http.MethodGet, http.StatusUnauthorized},
		{"/api/git/checkout", http.MethodPost, http.StatusForbidden},
		{"/api/worktree/list?dir=/r", http.MethodGet, http.StatusUnauthorized},
		{"/api/worktree/default-base-ref?dir=/r", http.MethodGet, http.StatusUnauthorized},
		{"/api/worktree/create-and-launch", http.MethodPost, http.StatusForbidden},
		{"/api/worktree/remove", http.MethodPost, http.StatusForbidden},
	} {
		if got := h.serve(tc.method, tc.path, "{}").Code; got != tc.guard {
			t.Errorf("%s %s = %d, want guard %d", tc.method, tc.path, got, tc.guard)
		}
		wrong := http.MethodPost
		if tc.method == http.MethodPost {
			wrong = http.MethodGet
		}
		if got := h.serve(wrong, tc.path, "").Code; got != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", wrong, tc.path, got)
		}
	}
	if len(h.host.calls) != 0 {
		t.Fatalf("guarded requests reached the host: %v", h.host.calls)
	}
}

// An explicit owner that is not connected never degrades to another host.
func TestExplicitOwnerFailsClosed(t *testing.T) {
	h := newHarness(module.Guards{Auth: pass, Localhost: pass})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/git/info?dirs=/r&remoteId=gone", ""},
		{http.MethodGet, "/api/git/branches?dir=/r&remoteId=gone", ""},
		{http.MethodGet, "/api/worktree/list?dir=/r&remoteId=gone", ""},
		{http.MethodGet, "/api/worktree/default-base-ref?dir=/r&remoteId=gone", ""},
		{http.MethodPost, "/api/worktree/remove", `{"projectDir":"/r","path":"/r/.wt/x","force":true,"remoteId":"gone"}`},
		{http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"gone","parentSessionId":"p"}`},
	} {
		if got := h.serve(tc.method, tc.path, tc.body).Code; got != http.StatusServiceUnavailable {
			t.Errorf("%s = %d, want 503", tc.path, got)
		}
	}
	if len(h.host.calls) != 0 || len(h.sent) != 0 {
		t.Fatalf("disconnected owner reached a host: %v sent=%d", h.host.calls, len(h.sent))
	}
}

func TestGitInfoRequiresOwnerAndAbsoluteDirs(t *testing.T) {
	h := newHarness(module.Guards{Auth: pass, Localhost: pass})
	for path, want := range map[string]int{
		"/api/git/info?dirs=/r":                            http.StatusBadRequest,
		"/api/git/info?dirs=/r,rel&remoteId=local":         http.StatusBadRequest,
		"/api/git/info?dirs=,&remoteId=local":              http.StatusBadRequest,
		"/api/git/info?dir=/a,b%20c&dirs=/x&remoteId=rem1": http.StatusOK,
	} {
		if got := h.serve(http.MethodGet, path, "").Code; got != want {
			t.Errorf("%s = %d, want %d", path, got, want)
		}
	}
	// dir is one literal path, commas and spaces kept, taking precedence.
	if len(h.host.calls) != 1 || h.host.calls[0] != "info:/a,b c" {
		t.Fatalf("calls = %v", h.host.calls)
	}
}

func TestErrorMapping(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body string
		err                      error
		want                     int
	}{
		{"checkout dirty", http.MethodPost, "/api/git/checkout", `{"dir":"/r","branch":"b"}`, git.ErrDirtyCheckout, http.StatusConflict},
		// A remote relays the sentinel as prose only.
		{"checkout dirty remote", http.MethodPost, "/api/git/checkout", `{"dir":"/r","branch":"b"}`, errors.New("rpc: would overwrite x"), http.StatusConflict},
		{"checkout failed", http.MethodPost, "/api/git/checkout", `{"dir":"/r","branch":"b"}`, errors.New("boom"), http.StatusBadGateway},
		{"checkout relative", http.MethodPost, "/api/git/checkout", `{"dir":"r","branch":"b"}`, nil, http.StatusBadRequest},
		{"diff not repo", http.MethodGet, "/api/git/diff?dir=/r", "", git.ErrNotRepo, http.StatusNotFound},
		{"diff canceled", http.MethodGet, "/api/git/diff?dir=/r", "", fmt.Errorf("git: %w", context.Canceled), 499},
		{"branches failed", http.MethodGet, "/api/git/branches?dir=/r", "", errors.New("boom"), http.StatusBadGateway},
		{"list not repo", http.MethodGet, "/api/worktree/list?dir=/r", "", git.ErrNotARepo, http.StatusNotFound},
		{"list failed", http.MethodGet, "/api/worktree/list?dir=/r", "", errors.New("boom"), http.StatusBadGateway},
		{"base not repo", http.MethodGet, "/api/worktree/default-base-ref?dir=/r", "", git.ErrNotARepo, http.StatusNotFound},
		// No usable base commit is an error, never an empty default.
		{"base unresolved", http.MethodGet, "/api/worktree/default-base-ref?dir=/r", "", errors.New("no commit"), http.StatusBadGateway},
		{"remove main", http.MethodPost, "/api/worktree/remove", `{"projectDir":"/r","path":"/r"}`, git.ErrMainWorktree, http.StatusConflict},
		{"remove dirty", http.MethodPost, "/api/worktree/remove", `{"projectDir":"/r","path":"/r/x"}`, git.ErrWorktreeDirty, http.StatusConflict},
		{"remove relative", http.MethodPost, "/api/worktree/remove", `{"projectDir":"/r","path":"x"}`, nil, http.StatusBadRequest},
		{"create elsewhere", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"rem1"}`, git.ErrBranchCheckedOutElsewhere, http.StatusConflict},
		{"create path conflict", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"rem1"}`, git.ErrPathConflict, http.StatusConflict},
		{"create not repo", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"rem1"}`, git.ErrNotARepo, http.StatusNotFound},
		{"create failed", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"rem1"}`, errors.New("boom"), http.StatusBadGateway},
		{"create no branch", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","remoteId":"rem1"}`, nil, http.StatusBadRequest},
		{"create no base", http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","newBranch":true,"remoteId":"rem1"}`, nil, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(module.Guards{Auth: pass, Localhost: pass})
			h.host.err = tc.err
			if got := h.serve(tc.method, tc.path, tc.body); got.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", got.Code, tc.want, got.Body)
			}
		})
	}
}

func TestDefaultBaseRefPassesFullyQualifiedRef(t *testing.T) {
	h := newHarness(module.Guards{Auth: pass, Localhost: pass})
	h.host.baseRef = "refs/remotes/origin/main"
	rr := h.serve(http.MethodGet, "/api/worktree/default-base-ref?dir=/r&remoteId=rem1", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"baseRef":"refs/remotes/origin/main"`) {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
}

// Inherited permissions reach the child before its first message, so the
// first turn already runs under the parent's posture.
func TestCreateAndLaunchAppliesPermissionsBeforeFirstSend(t *testing.T) {
	h := newHarness(module.Guards{Auth: pass, Localhost: pass})
	rr := h.serve(http.MethodPost, "/api/worktree/create-and-launch",
		`{"projectDir":"/r","autoName":true,"prompt":"fix it","remoteId":"rem1","parentSessionId":"parent",
		  "send":{"message":"hi","images":[{"url":"data:x","mime":"image/png"}],"model":"m","agent":"build"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body)
	}
	if got := strings.Join(h.steps, ","); got != "build:parent,apply:child,send" {
		t.Fatalf("steps = %s", got)
	}
	if !h.host.created.AutoName || h.host.created.Prompt != "fix it" {
		t.Fatalf("create request = %+v", h.host.created)
	}
	if h.sendTo != "r-rem1:opencode" || h.sent[0].SessionID != "child" || len(h.sent[0].Images) != 1 || h.sent[0].Agent != "build" {
		t.Fatalf("sent %q %+v", h.sendTo, h.sent)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]any{
		"sessionId": "child", "platform": "r-rem1:opencode", "remoteId": "rem1", "firstMessageSent": true,
		"firstMessageError": "", "worktreePath": "/r/.wt/x", "branch": "x", "permissionsInherited": true,
		"permissionsInheritedCount": float64(2), "permissionsInheritError": "",
	} {
		if body[k] != want {
			t.Errorf("%s = %v, want %v", k, body[k], want)
		}
	}
}

// A local target needs git on this machine; a remote checks its own.
func TestCreateAndLaunchRequiresLocalGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	h := newHarness(module.Guards{Auth: pass, Localhost: pass})
	if got := h.serve(http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"local"}`).Code; got != http.StatusServiceUnavailable {
		t.Fatalf("local = %d, want 503", got)
	}
	if got := h.serve(http.MethodPost, "/api/worktree/create-and-launch", `{"projectDir":"/r","branch":"x","remoteId":"rem1"}`).Code; got != http.StatusOK {
		t.Fatalf("remote = %d, want 200", got)
	}
	if SessionsAvailable(platforms.NewRegistry()) {
		t.Fatal("worktree sessions available without git, tmux or opencode")
	}
}
