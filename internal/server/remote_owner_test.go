package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/NoUseFreak/ocman/internal/dagu"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
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
			s := &Server{hostRouter: hostsvc.NewRouter(spy)}
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
// change: an empty or "local" remote ID must keep resolving to the hub.
func TestHandlersAcceptLocalOwner(t *testing.T) {
	for _, remoteID := range []string{"", "local"} {
		t.Run("remoteId="+remoteID, func(t *testing.T) {
			spy := &ownerSpy{}
			s := &Server{hostRouter: hostsvc.NewRouter(spy)}
			rr := httptest.NewRecorder()
			body := `{"dir":"/repo","remoteId":"` + remoteID + `"}`
			s.handleTermWindows(rr, httptest.NewRequest(http.MethodPost, "/api/term/windows", strings.NewReader(body)))
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 (body: %q)", rr.Code, rr.Body.String())
			}
			if spy.count() != 1 {
				t.Errorf("local host executed %d time(s); want 1", spy.count())
			}
		})
	}
}
