package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/term"
)

// ownerSpy is a Host that only records that it was asked to do work. It
// is registered as the *local* host, so any handler that degrades an
// unknown remote ID to the hub is caught executing here.
type ownerSpy struct {
	hostsvc.Host
	calls int32
}

func (h *ownerSpy) RemoteID() string { return "local" }

func (h *ownerSpy) hit()       { atomic.AddInt32(&h.calls, 1) }
func (h *ownerSpy) count() int { return int(atomic.LoadInt32(&h.calls)) }

func (h *ownerSpy) CreateWorktreeSession(context.Context, hostsvc.WorktreeSessionRequest) (*hostsvc.WorktreeSessionResult, error) {
	h.hit()
	return &hostsvc.WorktreeSessionResult{}, nil
}

func (h *ownerSpy) EnsureProjectOpencode(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.hit()
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:1234"}, nil
}

func (h *ownerSpy) TermWindows(context.Context, string) ([]hostsvc.TermWindow, error) {
	h.hit()
	return nil, nil
}

func (h *ownerSpy) TermCreateWindow(context.Context, string) (string, error) {
	h.hit()
	return "ocman-x-1", nil
}

func (h *ownerSpy) TermKillWindow(context.Context, string, string) error {
	h.hit()
	return nil
}

func (h *ownerSpy) TermAttach(context.Context, hostsvc.TermAttachRequest, hostsvc.TermConn) error {
	h.hit()
	return nil
}

func (h *ownerSpy) BeadsStatus(context.Context, string) (hostsvc.BeadsStatus, error) {
	h.hit()
	return hostsvc.BeadsStatus{}, nil
}

func (h *ownerSpy) DaguStatus(context.Context) dagu.Result {
	h.hit()
	return dagu.Result{}
}

// newOwnerTestServer builds the minimal Server these owner-routing tests
// need. s.sessions is stubbed over an empty registry rather than left
// nil: handleCreateSession dereferences it once owner resolution lets the
// request through, and a nil deref panics the whole test binary instead
// of failing one subtest with a readable message.
func newOwnerTestServer(spy *ownerSpy) *Server {
	return &Server{
		hostRouter: hostsvc.NewRouter(spy),
		sessions:   sessionsvc.New(platforms.NewRegistry(), sessionsvc.Hooks{}),
	}
}

// TestHandlersFailClosedOnUnknownRemote pins the fail-closed contract for
// every handler that takes an *explicit* owner from the client: a stale,
// disconnected or mistyped remote ID must never fall back to the hub —
// that runs destructive work (worktree creation, process launches, live
// shells) on the wrong machine. It must return 409 and execute nothing.
func TestHandlersFailClosedOnUnknownRemote(t *testing.T) {
	// A well-formed terminal window name for /term/windows DELETE, so the
	// owner check is what rejects the request, not window validation.
	const dir = "/remote/repo"

	tests := []struct {
		name   string
		invoke func(*Server, http.ResponseWriter)
	}{
		{
			name: "worktree create and launch",
			invoke: func(s *Server, w http.ResponseWriter) {
				body := `{"projectDir":"` + dir + `","branch":"feature/x","remoteId":"gone"}`
				s.handleWorktreeCreateAndLaunch(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
			},
		},
		{
			name: "tmux launch opencode",
			invoke: func(s *Server, w http.ResponseWriter) {
				body := `{"directory":"` + dir + `","remoteId":"gone"}`
				s.handleTmuxLaunchOpencode(w, httptest.NewRequest(http.MethodPost, "/api/tmux/launch-opencode", strings.NewReader(body)))
			},
		},
		{
			name: "terminal websocket attach",
			invoke: func(s *Server, w http.ResponseWriter) {
				s.handleTermWS(w, httptest.NewRequest(http.MethodGet, "/api/term/ws?dir="+dir+"&remoteId=gone", nil))
			},
		},
		{
			name: "terminal windows list",
			invoke: func(s *Server, w http.ResponseWriter) {
				s.handleTermWindows(w, httptest.NewRequest(http.MethodGet, "/api/term/windows?dir="+dir+"&remoteId=gone", nil))
			},
		},
		{
			name: "terminal window create",
			invoke: func(s *Server, w http.ResponseWriter) {
				body := `{"dir":"` + dir + `","remoteId":"gone"}`
				s.handleTermWindows(w, httptest.NewRequest(http.MethodPost, "/api/term/windows", strings.NewReader(body)))
			},
		},
		{
			name: "terminal window delete",
			invoke: func(s *Server, w http.ResponseWriter) {
				body := `{"dir":"` + dir + `","window":"` + term.WindowPrefix(dir) + `1","remoteId":"gone"}`
				s.handleTermWindows(w, httptest.NewRequest(http.MethodDelete, "/api/term/windows", strings.NewReader(body)))
			},
		},
		{
			name: "beads status",
			invoke: func(s *Server, w http.ResponseWriter) {
				s.handleProjectBeadsStatus(w, httptest.NewRequest(http.MethodGet, "/api/project/beads-status?dir="+dir+"&remoteId=gone", nil))
			},
		},
		{
			name: "dagu status",
			invoke: func(s *Server, w http.ResponseWriter) {
				s.handleDaguStatus(w, httptest.NewRequest(http.MethodGet, "/api/dagu/status?remoteId=gone", nil))
			},
		},
		{
			name: "create session on a remote platform",
			invoke: func(s *Server, w http.ResponseWriter) {
				body := `{"platform":"r-gone:opencode","directory":"` + dir + `"}`
				s.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body)))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spy := &ownerSpy{}
			// No remotes registered: "gone" is a disconnected remote.
			s := newOwnerTestServer(spy)
			rr := httptest.NewRecorder()
			tt.invoke(s, rr)

			if spy.count() != 0 {
				t.Errorf("local host executed %d time(s) for an unknown remote owner", spy.count())
			}
			// 503, never 409: two of these routes already use 409 for a
			// genuine domain conflict (dirty worktree, branch checked out
			// elsewhere) and the frontend tells them apart by the body
			// text. Reusing 409 here would make the status two-valued.
			if rr.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d; want 503 (body: %q)", rr.Code, rr.Body.String())
			}
			var env struct {
				Error struct {
					Code     string `json:"code"`
					RemoteID string `json:"remoteId"`
					Message  string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
				t.Fatalf("decode error envelope: %v (body: %q)", err, rr.Body.String())
			}
			if env.Error.Code != "remote_not_connected" {
				t.Errorf("error.code = %q; want %q", env.Error.Code, "remote_not_connected")
			}
			if env.Error.RemoteID != "gone" {
				t.Errorf("error.remoteId = %q; want %q", env.Error.RemoteID, "gone")
			}
			if env.Error.Message == "" {
				t.Error("error.message is empty; the frontend renders it verbatim")
			}
		})
	}
}

// TestHandlersAcceptLocalOwner guards the other side of the fail-closed
// change: an empty or "local" remote ID must keep resolving to the hub,
// on *every* handler that went through resolveOwner — a fail-closed
// helper that also rejected the local host would break the single-machine
// case, which is the common one.
//
// The universal assertion is "not rejected as an unconnected owner".
// Handlers that deterministically reach the host additionally assert the
// local host actually ran; handleTermWS can't (it needs a real WebSocket
// upgrade) and handleCreateSession's local path never carries a remote
// ID at all, so both only assert the absence of a rejection.
func TestHandlersAcceptLocalOwner(t *testing.T) {
	const dir = "/repo"

	tests := []struct {
		name string
		// skipUnless names a binary the local path requires; the subtest
		// is skipped when it isn't installed.
		skipUnless string
		// wantHostCall is false for handlers that stop before reaching
		// the host for reasons unrelated to owner resolution.
		wantHostCall bool
		invoke       func(*Server, http.ResponseWriter, string)
	}{
		{
			name:         "worktree create and launch",
			skipUnless:   "git",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				body := `{"projectDir":"` + dir + `","branch":"feature/x","remoteId":"` + rid + `"}`
				s.handleWorktreeCreateAndLaunch(w, httptest.NewRequest(http.MethodPost, "/api/worktree/create-and-launch", strings.NewReader(body)))
			},
		},
		{
			name:         "tmux launch opencode",
			skipUnless:   "tmux",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				body := `{"directory":"` + dir + `","remoteId":"` + rid + `"}`
				s.handleTmuxLaunchOpencode(w, httptest.NewRequest(http.MethodPost, "/api/tmux/launch-opencode", strings.NewReader(body)))
			},
		},
		{
			name: "terminal websocket attach",
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				s.handleTermWS(w, httptest.NewRequest(http.MethodGet, "/api/term/ws?dir="+dir+"&remoteId="+rid, nil))
			},
		},
		{
			name:         "terminal windows list",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				s.handleTermWindows(w, httptest.NewRequest(http.MethodGet, "/api/term/windows?dir="+dir+"&remoteId="+rid, nil))
			},
		},
		{
			name:         "terminal window create",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				body := `{"dir":"` + dir + `","remoteId":"` + rid + `"}`
				s.handleTermWindows(w, httptest.NewRequest(http.MethodPost, "/api/term/windows", strings.NewReader(body)))
			},
		},
		{
			name:         "terminal window delete",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				body := `{"dir":"` + dir + `","window":"` + term.WindowPrefix(dir) + `1","remoteId":"` + rid + `"}`
				s.handleTermWindows(w, httptest.NewRequest(http.MethodDelete, "/api/term/windows", strings.NewReader(body)))
			},
		},
		{
			name:         "beads status",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				s.handleProjectBeadsStatus(w, httptest.NewRequest(http.MethodGet, "/api/project/beads-status?dir="+dir+"&remoteId="+rid, nil))
			},
		},
		{
			name:         "dagu status",
			wantHostCall: true,
			invoke: func(s *Server, w http.ResponseWriter, rid string) {
				s.handleDaguStatus(w, httptest.NewRequest(http.MethodGet, "/api/dagu/status?remoteId="+rid, nil))
			},
		},
		{
			name: "create session on a local platform",
			invoke: func(s *Server, w http.ResponseWriter, _ string) {
				body := `{"platform":"opencode","directory":"` + dir + `"}`
				s.handleCreateSession(w, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body)))
			},
		},
	}

	for _, tt := range tests {
		for _, remoteID := range []string{"", "local"} {
			t.Run(tt.name+"/remoteId="+remoteID, func(t *testing.T) {
				if tt.skipUnless != "" {
					if _, err := exec.LookPath(tt.skipUnless); err != nil {
						t.Skipf("%s not available", tt.skipUnless)
					}
				}
				spy := &ownerSpy{}
				s := newOwnerTestServer(spy)
				rr := httptest.NewRecorder()
				tt.invoke(s, rr, remoteID)

				if strings.Contains(rr.Body.String(), "remote_not_connected") {
					t.Errorf("local owner rejected as unconnected: %d %q", rr.Code, rr.Body.String())
				}
				if tt.wantHostCall && spy.count() != 1 {
					t.Errorf("local host executed %d time(s); want 1 (status %d, body %q)", spy.count(), rr.Code, rr.Body.String())
				}
			})
		}
	}
}

// #533: request validation must run before the ensure side effect. A
// create naming a registered remote but an unknown platform must be
// rejected without launching a managed opencode instance on that remote.
func TestCreateSessionValidatesPlatformBeforeEnsure(t *testing.T) {
	local := &ownerSpy{}
	remoteSpy := &ownerSpy{}
	s := newOwnerTestServer(local)
	s.hostRouter.RegisterRemote("build-box", remoteSpy)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "r-build-box:opencode"})
	s.registry = reg
	s.sessions = sessionsvc.New(reg, sessionsvc.Hooks{})

	body := `{"platform":"r-build-box:bogus","directory":"/remote/repo"}`
	rr := httptest.NewRecorder()
	s.handleCreateSession(rr, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body)))

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d; want 400 (body: %q)", rr.Code, rr.Body.String())
	}
	if remoteSpy.count() != 0 {
		t.Errorf("remote host ensured %d time(s) for an invalid platform; want 0", remoteSpy.count())
	}
	if local.count() != 0 {
		t.Errorf("local host executed %d time(s); want 0", local.count())
	}
}

// The valid-platform remote create still ensures then creates.
func TestCreateSessionEnsuresForValidRemotePlatform(t *testing.T) {
	local := &ownerSpy{}
	remoteSpy := &ownerSpy{}
	s := newOwnerTestServer(local)
	s.hostRouter.RegisterRemote("build-box", remoteSpy)
	reg := platforms.NewRegistry()
	reg.Register(&fakePlatform{id: "r-build-box:opencode", createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
		return &platforms.CreateSessionResponse{ID: "new-sess"}, nil
	}})
	s.registry = reg
	s.sessions = sessionsvc.New(reg, sessionsvc.Hooks{})

	body := `{"platform":"r-build-box:opencode","directory":"/remote/repo"}`
	rr := httptest.NewRecorder()
	s.handleCreateSession(rr, httptest.NewRequest(http.MethodPost, "/api/sessions", strings.NewReader(body)))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 (body: %q)", rr.Code, rr.Body.String())
	}
	if remoteSpy.count() != 1 {
		t.Errorf("remote ensure calls = %d; want 1", remoteSpy.count())
	}
}
