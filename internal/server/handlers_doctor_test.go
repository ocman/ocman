package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
)

type doctorHost struct {
	hostsvc.Host
	checks []hostsvc.DoctorCheck
}

func (h doctorHost) Doctor(context.Context) []hostsvc.DoctorCheck { return h.checks }

func TestHandleDoctorMixedResults(t *testing.T) {
	srv := testServer(t)
	srv.hostRouter = hostsvc.NewRouter(doctorHost{checks: []hostsvc.DoctorCheck{
		{ID: "opencode", Required: true, OK: true, Detail: "/usr/bin/opencode"},
		{ID: "tmux", OK: false, Detail: "tmux not found on PATH", Hint: "brew install tmux"},
	}})
	srv.WithStartupIssues(StartupIssue{ID: "opencode-db-missing", Message: "OpenCode database not found at /x"}).
		WithToolPathError(errors.New("boom")).
		WithLogPath("/tmp/ocman.log")
	srv.mcpListenErr = "127.0.0.1:8227: address already in use"

	rr := httptest.NewRecorder()
	srv.handleDoctor(rr, httptest.NewRequest(http.MethodGet, "/api/doctor", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rr.Code, rr.Body.String())
	}
	var got struct {
		Checks  []hostsvc.DoctorCheck `json:"checks"`
		LogPath string                `json:"logPath"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.LogPath != "/tmp/ocman.log" {
		t.Errorf("logPath = %q", got.LogPath)
	}
	want := []struct {
		id string
		ok bool
	}{{"opencode-db", false}, {"opencode", true}, {"tmux", false}, {"mcp-listener", false}, {"login-shell-path", false}}
	if len(got.Checks) != len(want) {
		t.Fatalf("checks = %+v", got.Checks)
	}
	for i, w := range want {
		c := got.Checks[i]
		if c.ID != w.id || c.OK != w.ok {
			t.Errorf("check %d = %+v, want id=%s ok=%v", i, c, w.id, w.ok)
		}
		if !c.OK && c.ID != "tmux" && c.Hint == "" {
			t.Errorf("failing check %s has no hint", c.ID)
		}
	}
	if !got.Checks[0].Required {
		t.Error("opencode-db must be required")
	}
}

func TestDoctorChecksHealthy(t *testing.T) {
	srv := testServer(t)
	srv.mcpAddr = "127.0.0.1:8227"
	if c := srv.mcpListenerCheck(); !c.OK {
		t.Errorf("mcp-listener = %+v", c)
	}
	srv.mcpAddr = ""
	if c := srv.mcpListenerCheck(); c.OK || c.Detail == "" {
		t.Errorf("disabled mcp-listener = %+v", c)
	}
	if c := srv.loginShellPathCheck(); !c.OK {
		t.Errorf("login-shell-path = %+v", c)
	}
	srv.db = nil
	if c := srv.openCodeDBCheck(); c.OK {
		t.Errorf("opencode-db without db = %+v", c)
	}
}
