package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

type hookPlatform struct {
	*fakePlatform
	events    *[]providerprotocol.HookEvent
	installed *bool
	err       error
}

type hookHost struct {
	hostsvc.Host
	hookPlatform
}

func TestHookPluginChangesUseLocalHost(t *testing.T) {
	srv := testServer(t)
	var events []providerprotocol.HookEvent
	platform := hookPlatform{fakePlatform: &fakePlatform{id: "claudecode"}, events: &events, installed: new(bool)}
	host := &hookHost{hookPlatform: hookPlatform{installed: new(bool)}}
	srv.registry.Register(platform)
	srv.hostRouter = hostsvc.NewRouter(host)
	rec := httptest.NewRecorder()
	srv.handleHookPlugin(rec, httptest.NewRequest(http.MethodPost, "/api/hook-plugin/install", nil))
	if rec.Code != http.StatusOK || !*host.installed || *platform.installed {
		t.Fatalf("status=%d host installed=%v platform installed=%v", rec.Code, *host.installed, *platform.installed)
	}
}

func (p hookPlatform) Hook(_ context.Context, e providerprotocol.HookEvent) error {
	*p.events = append(*p.events, e)
	return p.err
}
func (p hookPlatform) HookPlugin() (bool, bool) { return true, *p.installed }
func (p hookPlatform) InstallHookPlugin(context.Context) error {
	*p.installed = p.err == nil
	return p.err
}
func (p hookPlatform) UninstallHookPlugin(context.Context) error {
	*p.installed = false
	return p.err
}

func TestHookReachesPlatformOnMCPListener(t *testing.T) {
	srv := testServer(t)
	srv.auth = newTestAuth(t, "hunter2")
	var events []providerprotocol.HookEvent
	srv.registry.Register(hookPlatform{fakePlatform: &fakePlatform{id: "claudecode"}, events: &events, installed: new(bool), err: errors.New("down")})
	srv.mcpAddr = "127.0.0.1:0"
	defer srv.startMCPListener()()

	resp, err := http.Post("http://"+srv.mcpAddr+"/hook", "application/json", strings.NewReader(`{"event":"Stop","sessionID":"s1"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || len(events) != 1 || events[0] != (providerprotocol.HookEvent{Event: "Stop", SessionID: "s1"}) {
		t.Fatalf("status %d, events %+v", resp.StatusCode, events)
	}
	resp, err = http.Post("http://"+srv.mcpAddr+"/hook", "application/json", strings.NewReader(`nope`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid body status = %d", resp.StatusCode)
	}
}

func TestHookWithoutPlatformIsAccepted(t *testing.T) {
	srv := testServer(t)
	rec := httptest.NewRecorder()
	srv.handleHook(rec, httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"event":"Stop","sessionID":"s1"}`)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestHookPluginInstallAndUninstall(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path string) (int, map[string]bool) {
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		var got map[string]bool
		_ = json.Unmarshal(rec.Body.Bytes(), &got)
		return rec.Code, got
	}
	if code, got := call(http.MethodGet, "/api/hook-plugin"); code != http.StatusOK || got["available"] {
		t.Fatalf("no platform: %d %v", code, got)
	}

	var events []providerprotocol.HookEvent
	p := hookPlatform{fakePlatform: &fakePlatform{id: "claudecode"}, events: &events, installed: new(bool)}
	srv.registry.Register(p)
	if code, got := call(http.MethodPost, "/api/hook-plugin/install"); code != http.StatusOK || !got["available"] || !got["installed"] {
		t.Fatalf("install: %d %v", code, got)
	}
	if code, got := call(http.MethodPost, "/api/hook-plugin/uninstall"); code != http.StatusOK || got["installed"] {
		t.Fatalf("uninstall: %d %v", code, got)
	}
	if code, _ := call(http.MethodGet, "/api/hook-plugin/install"); code != http.StatusMethodNotAllowed {
		t.Fatalf("GET install = %d", code)
	}

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/hook-plugin/install", nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback status = %d", rec.Code)
	}
}

func TestHookPluginInstallFailure(t *testing.T) {
	srv := testServer(t)
	var events []providerprotocol.HookEvent
	srv.registry.Register(hookPlatform{fakePlatform: &fakePlatform{id: "claudecode"}, events: &events, installed: new(bool), err: errors.New("claude plugin install: exit 1")})
	rec := httptest.NewRecorder()
	srv.handleHookPlugin(rec, httptest.NewRequest(http.MethodPost, "/api/hook-plugin/install", nil))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "exit 1") {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
}
