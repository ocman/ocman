package server

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/tmux"
)

// fakeSwitchRunner builds a tmux.SwitchRunner suitable for unit-testing
// handleTmuxSwitchWith without requiring a real tmux binary.
type fakeSwitchRunner struct {
	sessions     []tmux.Session
	windows      map[string][]tmux.Window
	clients      []tmux.Client
	listSessErr  error
	listCliErr   error
	listWinErr   error
	switchedTTY  string
	switchedSess string
	switchErr    error
}

func (f *fakeSwitchRunner) toRunner() tmux.SwitchRunner {
	return tmux.SwitchRunner{
		ListSessions: func() ([]tmux.Session, error) {
			return f.sessions, f.listSessErr
		},
		ListWindows: func(sessionName string) ([]tmux.Window, error) {
			if f.listWinErr != nil {
				return nil, f.listWinErr
			}
			return f.windows[sessionName], nil
		},
		ListClients: func() ([]tmux.Client, error) {
			return f.clients, f.listCliErr
		},
		SwitchClient: func(tty, sess string) error {
			f.switchedTTY = tty
			f.switchedSess = sess
			return f.switchErr
		},
	}
}

// newSwitchTestServer returns a minimal Server and a pre-wired runner
// stub. isTmuxAvailable is not called in handleTmuxSwitchWith — the
// outer handleTmuxSwitch wrapper checks it; the *With variant is called
// directly by tests so we skip that guard.
func newSwitchTestServer() *Server {
	return &Server{}
}

func TestHandleTmuxSwitch_MissingSession(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{}
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing session, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTmuxSwitch_SessionNotFound(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "other", ResolvedPath: "/other"}},
		clients:  []tmux.Client{{TTY: "/dev/pts/0", Session: "other"}},
	}
	body := strings.NewReader(`{"session":"myproject","client":"/dev/pts/0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleTmuxSwitch_SingleClient verifies that when no client TTY is
// provided and exactly one tmux client is connected, the server resolves
// the TTY automatically and performs the switch. This is the primary fix
// for issue #25: the old code defaulted to /dev/ttys000 (macOS-only) and
// failed on Linux or when multiple terminals were open.
func TestHandleTmuxSwitch_SingleClient_NoTTYInRequest(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients:  []tmux.Client{{TTY: "/dev/pts/3", Session: "myproject"}},
	}
	body := strings.NewReader(`{"session":"myproject"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if fake.switchedTTY != "/dev/pts/3" {
		t.Errorf("switchedTTY = %q, want /dev/pts/3", fake.switchedTTY)
	}
	if fake.switchedSess != "myproject" {
		t.Errorf("switchedSess = %q, want myproject", fake.switchedSess)
	}
}

// TestHandleTmuxSwitch_LinuxPTY verifies that Linux-style /dev/pts/N TTYs
// are accepted. The old /dev/ttys000 default would fail on Linux because
// PTYs are /dev/pts/N there.
func TestHandleTmuxSwitch_LinuxPTY(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "proj", ResolvedPath: "/home/user/proj"}},
		clients:  []tmux.Client{{TTY: "/dev/pts/7", Session: "proj"}},
	}
	body := strings.NewReader(`{"session":"proj","client":"/dev/pts/7"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for Linux PTY, got %d: %s", rr.Code, rr.Body.String())
	}
	if fake.switchedTTY != "/dev/pts/7" {
		t.Errorf("switchedTTY = %q, want /dev/pts/7", fake.switchedTTY)
	}
}

// TestHandleTmuxSwitch_NoClientsConnected verifies that when no client TTY
// is provided and no tmux clients are connected, the server returns 400
// instead of defaulting to /dev/ttys000.
func TestHandleTmuxSwitch_NoClientsConnected(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients:  []tmux.Client{},
	}
	body := strings.NewReader(`{"session":"myproject"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for no clients, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleTmuxSwitch_MultipleClientsNoTTY verifies that when multiple
// tmux clients are connected and no client TTY is given, the server
// returns 400 (the frontend should have shown a picker). This prevents
// the old ambiguity where the server would silently target the wrong
// terminal.
func TestHandleTmuxSwitch_MultipleClientsNoTTY(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients: []tmux.Client{
			{TTY: "/dev/ttys001", Session: "myproject"},
			{TTY: "/dev/ttys002", Session: "other"},
		},
	}
	body := strings.NewReader(`{"session":"myproject"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for multiple clients without TTY, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestHandleTmuxSwitch_MultipleClients_ExplicitTTY verifies that when
// multiple clients are connected, an explicit client TTY in the request
// is honoured.
func TestHandleTmuxSwitch_MultipleClients_ExplicitTTY(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients: []tmux.Client{
			{TTY: "/dev/ttys001", Session: "myproject"},
			{TTY: "/dev/ttys002", Session: "other"},
		},
	}
	body := strings.NewReader(`{"session":"myproject","client":"/dev/ttys002"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for explicit TTY with multiple clients, got %d: %s", rr.Code, rr.Body.String())
	}
	if fake.switchedTTY != "/dev/ttys002" {
		t.Errorf("switchedTTY = %q, want /dev/ttys002", fake.switchedTTY)
	}
}

func TestHandleTmuxSwitch_ClientNotFound(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients:  []tmux.Client{{TTY: "/dev/pts/0", Session: "myproject"}},
	}
	body := strings.NewReader(`{"session":"myproject","client":"/dev/pts/9"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown client TTY, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTmuxSwitch_InvalidTTY(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients:  []tmux.Client{{TTY: "/dev/pts/0", Session: "myproject"}},
	}
	// /dev/ttys000 is invalid on Linux; test that we reject non-matching TTY paths
	body := strings.NewReader(`{"session":"myproject","client":"/tmp/evil"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid TTY path, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestHandleTmuxSwitch_WindowTarget(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions: []tmux.Session{{Name: "proj", ResolvedPath: "/src/proj"}},
		windows: map[string][]tmux.Window{
			"proj": {{Name: "wt-feature", Path: "/src/proj/.worktrees/feature"}},
		},
		clients: []tmux.Client{{TTY: "/dev/pts/1", Session: "proj"}},
	}
	body := strings.NewReader(`{"session":"proj:wt-feature","client":"/dev/pts/1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for session:window target, got %d: %s", rr.Code, rr.Body.String())
	}
	if fake.switchedSess != "proj:wt-feature" {
		t.Errorf("switchedSess = %q, want proj:wt-feature", fake.switchedSess)
	}
}

func TestHandleTmuxSwitch_SwitchError(t *testing.T) {
	srv := newSwitchTestServer()
	fake := &fakeSwitchRunner{
		sessions:  []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}},
		clients:   []tmux.Client{{TTY: "/dev/pts/0", Session: "myproject"}},
		switchErr: errors.New("tmux: no server running"),
	}
	body := strings.NewReader(`{"session":"myproject","client":"/dev/pts/0"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/switch", body)
	rr := httptest.NewRecorder()
	srv.handleTmuxSwitchWith(rr, req, fake.toRunner())
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when switch fails, got %d: %s", rr.Code, rr.Body.String())
	}
}

// TestValidTTYPath exercises the TTY path regex against both macOS and
// Linux style device paths.
func TestValidTTYPath(t *testing.T) {
	valid := []string{
		"/dev/ttys000",
		"/dev/ttys001",
		"/dev/tty0",
		"/dev/pts/0",
		"/dev/pts/12",
	}
	invalid := []string{
		"",
		"/dev/ttys000x",
		"/dev/pts/",
		"/tmp/evil",
		"pts/0",
		"/dev/pts/abc",
	}
	for _, v := range valid {
		if !tmux.ValidTTYPath.MatchString(v) {
			t.Errorf("tmux.ValidTTYPath should match %q", v)
		}
	}
	for _, v := range invalid {
		if tmux.ValidTTYPath.MatchString(v) {
			t.Errorf("tmux.ValidTTYPath should not match %q", v)
		}
	}
}
