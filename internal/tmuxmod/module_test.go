package tmuxmod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/testutil"
	"github.com/NoUseFreak/ocman/internal/tmux"
)

// fakeHost implements only what the module may touch; the embedded nil
// Host panics on anything else.
type fakeHost struct {
	hostsvc.Host
	sessions  []hostsvc.TmuxSession
	ensureErr error
	ensured   []string
}

func (h *fakeHost) TmuxSessions(context.Context) ([]hostsvc.TmuxSession, error) {
	return h.sessions, nil
}

func (h *fakeHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	h.ensured = append(h.ensured, req.ProjectDir)
	if h.ensureErr != nil {
		return nil, h.ensureErr
	}
	return &hostsvc.EnsureProjectOpencodeResult{Runtime: ocruntime.Instance{ID: "managed-1"}}, nil
}

func passthrough(h http.HandlerFunc) http.HandlerFunc { return h }

func newTestModule(host *fakeHost, runner tmux.SwitchRunner) *Module {
	return NewModule(Deps{
		Local: func() hostsvc.Host { return host },
		ResolveOwner: func(w http.ResponseWriter, _, remoteID string) (hostsvc.Host, bool) {
			if remoteID == "gone" {
				http.Error(w, "remote gone is not connected", http.StatusServiceUnavailable)
				return nil, false
			}
			return host, true
		},
		Guards: module.Guards{Auth: passthrough, Localhost: passthrough},
		Runner: runner,
	})
}

type muxRouter struct{ mux *http.ServeMux }

func (r muxRouter) Handle(p string, h http.HandlerFunc)       { r.mux.HandleFunc(p, h) }
func (r muxRouter) Local(p string, h http.HandlerFunc)        { r.mux.HandleFunc(p, h) }
func (r muxRouter) Setting(p string, h http.HandlerFunc)      { r.mux.HandleFunc(p, h) }
func (r muxRouter) LocalSetting(p string, h http.HandlerFunc) { r.mux.HandleFunc(p, h) }

func serve(m *Module, method, path, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	m.Routes(muxRouter{mux})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// Every route sits behind the localhost guard, after the method check.
func TestRoutesUseLocalhostGuardAfterMethod(t *testing.T) {
	var guarded []string
	m := NewModule(Deps{Guards: module.Guards{Localhost: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			guarded = append(guarded, r.URL.Path)
			http.Error(w, "forbidden", http.StatusForbidden)
		}
	}}})
	routes := map[string]string{
		"/api/tmux/clients":         http.MethodGet,
		"/api/tmux/sessions":        http.MethodGet,
		"/api/tmux/switch":          http.MethodPost,
		"/api/tmux/launch-opencode": http.MethodPost,
	}
	for path, method := range routes {
		wrong := http.MethodPost
		if method == http.MethodPost {
			wrong = http.MethodGet
		}
		if rec := serve(m, wrong, path, ""); rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", wrong, path, rec.Code)
		}
		if rec := serve(m, method, path, "{}"); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want the guard's 403", method, path, rec.Code)
		}
	}
	if len(guarded) != len(routes) {
		t.Fatalf("guard ran for %v; want once per route and never on a wrong method", guarded)
	}
}

func TestStatus_NoServerReportsAvailable(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (No such file or directory)")
	m := NewModule(Deps{Local: func() hostsvc.Host { return tmuxErrHost{} }, Guards: module.Guards{Localhost: passthrough}})

	for _, path := range []string{"/api/tmux/sessions", "/api/tmux/clients"} {
		var got map[string]json.RawMessage
		rec := serve(m, http.MethodGet, path, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if string(got["available"]) != "true" {
			t.Errorf("%s available = %s, want true", path, got["available"])
		}
		for k, v := range got {
			if k != "available" && string(v) != "[]" {
				t.Errorf("%s %s = %s, want []", path, k, v)
			}
		}
	}
}

func TestStatus_ConnectionPermissionErrorReportsUnavailable(t *testing.T) {
	installFakeTmux(t, "error connecting to /private/tmp/tmux-501/default (Permission denied)")
	m := NewModule(Deps{Local: func() hostsvc.Host { return tmuxErrHost{} }, Guards: module.Guards{Localhost: passthrough}})

	for _, path := range []string{"/api/tmux/sessions", "/api/tmux/clients"} {
		var got struct{ Available bool }
		rec := serve(m, http.MethodGet, path, "")
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.Available {
			t.Errorf("%s available = true, want false", path)
		}
	}
}

// tmuxErrHost lists sessions with the real tmux, so the fake binary's
// error reaches the handler the way the local Host returns it.
type tmuxErrHost struct{ hostsvc.Host }

func (tmuxErrHost) TmuxSessions(ctx context.Context) ([]hostsvc.TmuxSession, error) {
	_, err := tmux.ListSessions(ctx)
	return nil, err
}

// Sessions come from the Host seam, not tmux directly: a bypass would
// return the fake binary's decoy row. The wire shape keeps resolvedPath.
func TestSessions_RoutesThroughHostSeam(t *testing.T) {
	dir := t.TempDir()
	testutil.WriteScript(t, filepath.Join(dir, "tmux"), "#!/bin/sh\n[ \"$1\" = list-sessions ] && printf 'decoy|/tmp/decoy|1\\n'\nexit 0\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	m := newTestModule(&fakeHost{sessions: []hostsvc.TmuxSession{{Name: "~/src/ocman", Path: "/home/u/src/ocman"}}}, tmux.SwitchRunner{})
	rec := serve(m, http.MethodGet, "/api/tmux/sessions", "")
	want := `{"available":true,"sessions":[{"name":"~/src/ocman","resolvedPath":"/home/u/src/ocman"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestLaunchOpencode(t *testing.T) {
	tests := []struct {
		name, body string
		ensureErr  error
		want       int
		wantBody   string
		wantCalls  int
	}{
		{name: "remote owner ensures", body: `{"directory":"/r/repo","remoteId":"abc"}`, want: 200, wantBody: `{"session":"managed-1"}`, wantCalls: 1},
		{name: "disconnected owner fails closed", body: `{"directory":"/r/repo","remoteId":"gone"}`, want: 503},
		{name: "ensure error is a 500", body: `{"directory":"/r/repo","remoteId":"abc"}`, ensureErr: errors.New("boom"), want: 500, wantCalls: 1},
		{name: "cancelled ensure is a 499", body: `{"directory":"/r/repo","remoteId":"abc"}`, ensureErr: context.Canceled, want: 499, wantCalls: 1},
		{name: "directory required", body: `{"remoteId":"abc"}`, want: 400},
		{name: "relative directory", body: `{"directory":"repo","remoteId":"abc"}`, want: 400},
		{name: "invalid JSON", body: `{`, want: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := &fakeHost{ensureErr: tt.ensureErr}
			rec := serve(newTestModule(host, tmux.SwitchRunner{}), http.MethodPost, "/api/tmux/launch-opencode", tt.body)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.wantBody != "" && strings.TrimSpace(rec.Body.String()) != tt.wantBody {
				t.Errorf("body = %s, want %s", rec.Body.String(), tt.wantBody)
			}
			if len(host.ensured) != tt.wantCalls {
				t.Errorf("ensure calls = %v, want %d", host.ensured, tt.wantCalls)
			}
		})
	}
}

// A local launch needs tmux on this machine; a remote one does not.
func TestLaunchOpencode_LocalWithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	host := &fakeHost{}
	for _, rid := range []string{"", "local"} {
		rec := serve(newTestModule(host, tmux.SwitchRunner{}), http.MethodPost, "/api/tmux/launch-opencode", `{"directory":"/repo","remoteId":"`+rid+`"}`)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("remoteId=%q status = %d, want 503", rid, rec.Code)
		}
	}
	if len(host.ensured) != 0 {
		t.Fatalf("ensured %v without tmux", host.ensured)
	}
}

func TestSwitch_WithoutTmux(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	rec := serve(newTestModule(&fakeHost{}, tmux.SwitchRunner{}), http.MethodPost, "/api/tmux/switch", `{"session":"x"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
}

// fakeRunner is a tmux.SwitchRunner over fixed rows.
type fakeRunner struct {
	sessions     []tmux.Session
	windows      map[string][]tmux.Window
	clients      []tmux.Client
	listSessErr  error
	listCliErr   error
	listWinErr   error
	switchErr    error
	switchedTTY  string
	switchedSess string
}

func (f *fakeRunner) runner() tmux.SwitchRunner {
	return tmux.SwitchRunner{
		ListSessions: func(context.Context) ([]tmux.Session, error) { return f.sessions, f.listSessErr },
		ListWindows: func(_ context.Context, s string) ([]tmux.Window, error) {
			return f.windows[s], f.listWinErr
		},
		ListClients: func(context.Context) ([]tmux.Client, error) { return f.clients, f.listCliErr },
		SwitchClient: func(_ context.Context, tty, sess string) error {
			f.switchedTTY, f.switchedSess = tty, sess
			return f.switchErr
		},
	}
}

func TestSwitchClient(t *testing.T) {
	proj := []tmux.Session{{Name: "myproject", ResolvedPath: "/src/myproject"}}
	one := []tmux.Client{{TTY: "/dev/pts/0", Session: "myproject"}}
	two := []tmux.Client{{TTY: "/dev/ttys001", Session: "myproject"}, {TTY: "/dev/ttys002", Session: "other"}}
	boom := errors.New("boom")

	tests := []struct {
		name     string
		fake     fakeRunner
		body     string
		want     int
		wantTTY  string
		wantSess string
	}{
		{name: "missing session", body: `{}`, want: 400},
		{name: "invalid JSON", body: `{`, want: 400},
		{name: "invalid session name", body: `{"session":"a;rm -rf /"}`, want: 400},
		{name: "overlong session name", body: `{"session":"` + strings.Repeat("a", 513) + `"}`, want: 400},
		{name: "session not found", fake: fakeRunner{sessions: []tmux.Session{{Name: "other"}}, clients: one}, body: `{"session":"myproject","client":"/dev/pts/0"}`, want: 404},
		{name: "list sessions error", fake: fakeRunner{listSessErr: boom}, body: `{"session":"myproject"}`, want: 500},
		// #25: no default TTY; infer only from exactly one client.
		{name: "single client inferred", fake: fakeRunner{sessions: proj, clients: []tmux.Client{{TTY: "/dev/pts/3"}}}, body: `{"session":"myproject"}`, want: 204, wantTTY: "/dev/pts/3", wantSess: "myproject"},
		{name: "linux pty", fake: fakeRunner{sessions: proj, clients: []tmux.Client{{TTY: "/dev/pts/7"}}}, body: `{"session":"myproject","client":"/dev/pts/7"}`, want: 204, wantTTY: "/dev/pts/7"},
		{name: "no clients", fake: fakeRunner{sessions: proj}, body: `{"session":"myproject"}`, want: 400},
		{name: "multiple clients without tty", fake: fakeRunner{sessions: proj, clients: two}, body: `{"session":"myproject"}`, want: 400},
		{name: "multiple clients explicit tty", fake: fakeRunner{sessions: proj, clients: two}, body: `{"session":"myproject","client":"/dev/ttys002"}`, want: 204, wantTTY: "/dev/ttys002"},
		{name: "client not found", fake: fakeRunner{sessions: proj, clients: one}, body: `{"session":"myproject","client":"/dev/pts/9"}`, want: 404},
		{name: "invalid tty", fake: fakeRunner{sessions: proj, clients: one}, body: `{"session":"myproject","client":"/tmp/evil"}`, want: 400},
		{name: "list clients error", fake: fakeRunner{sessions: proj, listCliErr: boom}, body: `{"session":"myproject"}`, want: 500},
		{name: "window target", fake: fakeRunner{sessions: []tmux.Session{{Name: "proj"}}, windows: map[string][]tmux.Window{"proj": {{Name: "wt-feature"}}}, clients: one}, body: `{"session":"proj:wt-feature","client":"/dev/pts/0"}`, want: 204, wantSess: "proj:wt-feature"},
		{name: "window not found", fake: fakeRunner{sessions: []tmux.Session{{Name: "proj"}}, clients: one}, body: `{"session":"proj:nope","client":"/dev/pts/0"}`, want: 404},
		{name: "list windows error", fake: fakeRunner{sessions: []tmux.Session{{Name: "proj"}}, listWinErr: boom}, body: `{"session":"proj:w"}`, want: 500},
		{name: "switch error", fake: fakeRunner{sessions: proj, clients: one, switchErr: boom}, body: `{"session":"myproject","client":"/dev/pts/0"}`, want: 500},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestModule(&fakeHost{}, tt.fake.runner())
			rec := httptest.NewRecorder()
			m.switchClient(rec, httptest.NewRequest(http.MethodPost, "/api/tmux/switch", strings.NewReader(tt.body)))
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
			if tt.wantTTY != "" && tt.fake.switchedTTY != tt.wantTTY {
				t.Errorf("switched tty = %q, want %q", tt.fake.switchedTTY, tt.wantTTY)
			}
			if tt.wantSess != "" && tt.fake.switchedSess != tt.wantSess {
				t.Errorf("switched session = %q, want %q", tt.fake.switchedSess, tt.wantSess)
			}
		})
	}
}

func TestValidTTYPath(t *testing.T) {
	for _, v := range []string{"/dev/ttys000", "/dev/ttys001", "/dev/tty0", "/dev/pts/0", "/dev/pts/12"} {
		if !tmux.ValidTTYPath.MatchString(v) {
			t.Errorf("should match %q", v)
		}
	}
	for _, v := range []string{"", "/dev/ttys000x", "/dev/pts/", "/tmp/evil", "pts/0", "/dev/pts/abc"} {
		if tmux.ValidTTYPath.MatchString(v) {
			t.Errorf("should not match %q", v)
		}
	}
}

func installFakeTmux(t *testing.T, stderr string) {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteScript(t, filepath.Join(dir, "tmux"), fmt.Sprintf(`#!/bin/sh
case "$1" in
  list-sessions|list-clients|list-windows)
    printf '%%s\n' %q >&2
    exit 1
    ;;
  *)
    exit 0
    ;;
esac
`, stderr))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
