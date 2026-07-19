package server

import (
	"bytes"
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
	srv.handleTmuxLaunchOpencode(rec, req)

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

func TestHandleTermWindowsRoutesToRemoteHost(t *testing.T) {
	srv := testServer(t)
	var listedDir, createdDir, killedDir, killedWin string
	srv.HostRouter().RegisterRemote("abc", local.New(local.Deps{
		TermWindows: func(dir string) ([]hostsvc.TermWindow, error) {
			listedDir = dir
			return []hostsvc.TermWindow{{Name: "ocman-abc-1", Title: "vim"}}, nil
		},
		TermCreateWindow: func(dir string) (string, error) {
			createdDir = dir
			return "ocman-abc-2", nil
		},
		TermKillWindow: func(dir, window string) error {
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
