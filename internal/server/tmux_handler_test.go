package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
	"github.com/NoUseFreak/ocman/internal/term"
)

// The manual new-session bootstrap (POST /api/tmux/launch-opencode) must
// route through EnsureProjectOpencode — not the raw tmux launcher — so it
// shares the singleflight guard and managed registry with the automatic
// launch paths and cannot create a competing instance (#376 AC-3). We
// prove the handler funnels through EnsureProjectOpencode by asserting the
// returned "session" is the managed runtime's instance ID.
func TestHandleTmuxLaunchOpencodeRoutesThroughEnsure(t *testing.T) {
	repo := initWorktreeTestRepo(t)
	srv := testServer(t)
	srv.HostRouter().RegisterRemote("abc", local.New(local.Deps{
		Runtime: fakeRuntime{endpoint: "http://127.0.0.1:5599"},
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tmux/launch-opencode",
		bytes.NewBufferString(`{"directory":"`+repo+`","remoteId":"abc"}`))
	rec := httptest.NewRecorder()
	serveTmux(srv, rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// fakeRuntime.Launch returns an instance with ID "sess-name"; the
	// handler surfaces the managed runtime ID as "session".
	if got["session"] != "sess-name" {
		t.Fatalf("session = %q, want sess-name (managed runtime ID)", got["session"])
	}
}

// serveTmux sends req through the tmux module's routes as a loopback,
// origin-less client, so the server's real guards and owner resolution run.
func serveTmux(s *Server, w http.ResponseWriter, req *http.Request) {
	mux := http.NewServeMux()
	s.tmuxModule().Routes(moduleRouter{s: s, mux: mux})
	req.RemoteAddr = "127.0.0.1:12345"
	mux.ServeHTTP(w, req)
}

// The tmux routes are mounted from the module registry with the method
// check ahead of the localhost guard, as when routes.go registered them.
func TestTmuxRoutesMountedBehindGuards(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, path, remote string
		want                 int
	}{
		{http.MethodPost, "/api/tmux/clients", "127.0.0.1:1", http.StatusMethodNotAllowed},
		{http.MethodPost, "/api/tmux/sessions", "192.0.2.1:1", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/tmux/switch", "127.0.0.1:1", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/tmux/launch-opencode", "127.0.0.1:1", http.StatusMethodNotAllowed},
		{http.MethodGet, "/api/tmux/clients", "192.0.2.1:1", http.StatusForbidden},
		{http.MethodGet, "/api/tmux/sessions", "192.0.2.1:1", http.StatusForbidden},
		{http.MethodPost, "/api/tmux/switch", "192.0.2.1:1", http.StatusForbidden},
		{http.MethodPost, "/api/tmux/launch-opencode", "192.0.2.1:1", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path+" from "+tt.remote, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(`{}`))
			req.RemoteAddr = tt.remote
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tt.want, rec.Body.String())
			}
		})
	}

	// A cross-origin browser on loopback is refused too.
	req := httptest.NewRequest(http.MethodPost, "/api/tmux/launch-opencode", bytes.NewBufferString(`{}`))
	req.RemoteAddr = "127.0.0.1:1"
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", rec.Code)
	}
}

func TestHandleTermWindowsRoutesToRemoteHost(t *testing.T) {
	srv := testServer(t)
	var listedDir, createdDir, killedDir, killedWin string
	srv.HostRouter().RegisterRemote("abc", local.New(local.Deps{
		TermWindows: func(_ context.Context, dir string) ([]hostsvc.TermWindow, error) {
			listedDir = dir
			return []hostsvc.TermWindow{{Name: "ocman-abc-1", Title: "vim"}}, nil
		},
		TermCreateWindow: func(_ context.Context, dir string) (string, error) {
			createdDir = dir
			return "ocman-abc-2", nil
		},
		TermKillWindow: func(_ context.Context, dir, window string) error {
			killedDir, killedWin = dir, window
			return nil
		},
	}))

	// GET (list) routes to the remote via ?remoteId.
	listReq := httptest.NewRequest(http.MethodGet, "/api/term/windows?dir=/remote/repo&remoteId=abc", nil)
	listRec := httptest.NewRecorder()
	srv.handleTermWindows(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d: %s", listRec.Code, listRec.Body.String())
	}
	if listedDir != "/remote/repo" {
		t.Fatalf("listed dir = %q", listedDir)
	}

	// POST (create) routes to the remote via body remoteId.
	createReq := httptest.NewRequest(http.MethodPost, "/api/term/windows",
		bytes.NewBufferString(`{"dir":"/remote/repo","remoteId":"abc"}`))
	createRec := httptest.NewRecorder()
	srv.handleTermWindows(createRec, createReq)
	if createRec.Code != http.StatusOK {
		t.Fatalf("create status = %d: %s", createRec.Code, createRec.Body.String())
	}
	if createdDir != "/remote/repo" {
		t.Fatalf("created dir = %q", createdDir)
	}

	// DELETE (kill) routes to the remote; the window must be well-formed
	// for the dir (validated on the hub before delegating).
	win := term.WindowPrefix("/remote/repo") + "1"
	killReq := httptest.NewRequest(http.MethodDelete, "/api/term/windows",
		bytes.NewBufferString(`{"dir":"/remote/repo","window":"`+win+`","remoteId":"abc"}`))
	killRec := httptest.NewRecorder()
	srv.handleTermWindows(killRec, killReq)
	if killRec.Code != http.StatusNoContent {
		t.Fatalf("kill status = %d: %s", killRec.Code, killRec.Body.String())
	}
	if killedDir != "/remote/repo" || killedWin != win {
		t.Fatalf("killed dir/window = %q/%q", killedDir, killedWin)
	}
}
