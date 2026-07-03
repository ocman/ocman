package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc/local"
)

func TestHandleTmuxLaunchOpencodeRoutesToRemoteHost(t *testing.T) {
	srv := testServer(t)
	var launchedDir string
	srv.HostRouter().RegisterRemote("abc", local.New(local.Deps{
		LaunchTmux: func(directory string) (string, error) {
			launchedDir = directory
			return "remote-session", nil
		},
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/tmux/launch-opencode", bytes.NewBufferString(`{"directory":"/remote/repo","remoteId":"abc"}`))
	rec := httptest.NewRecorder()
	srv.handleTmuxLaunchOpencode(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if launchedDir != "/remote/repo" {
		t.Fatalf("launched dir = %q, want /remote/repo", launchedDir)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got["session"] != "remote-session" {
		t.Fatalf("session = %q, want remote-session", got["session"])
	}
}
