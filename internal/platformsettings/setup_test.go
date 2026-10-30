package platformsettings

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/module"
)

type setupHost struct {
	err      error
	installs int
}

func (h *setupHost) CheckPlatformSetup(context.Context, string) (hostsvc.PlatformSetup, error) {
	return hostsvc.PlatformSetup{Ready: true}, h.err
}
func (h *setupHost) InstallPlatformSetup(context.Context, string) (hostsvc.PlatformSetup, error) {
	h.installs++
	return hostsvc.PlatformSetup{Ready: true}, h.err
}

type setupRouter struct {
	module.Router
	mux *http.ServeMux
}

func (r setupRouter) LocalSetting(path string, h http.HandlerFunc) { r.mux.HandleFunc(path, h) }

func TestSetupRoutes(t *testing.T) {
	host := &setupHost{}
	m := New(nil).WithSetup(func() hostsvc.PlatformSetupManager { return host })
	mux := http.NewServeMux()
	m.Routes(setupRouter{mux: mux})
	for _, tc := range []struct {
		method, path string
		code         int
	}{
		{"GET", "/api/settings/platforms/codex/setup", 200},
		{"POST", "/api/settings/platforms/codex/install", 200},
		{"GET", "/api/settings/platforms/other/setup", 400},
		{"GET", "/api/settings/platforms/codex/install", 405},
		{"POST", "/api/settings/platforms/codex/setup", 405},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
		if w.Code != tc.code {
			t.Fatalf("%+v: %d %s", tc, w.Code, w.Body.String())
		}
	}
	if host.installs != 1 {
		t.Fatal("install not explicit")
	}
	host.err = fmt.Errorf("check failed")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/platforms/codex/setup", nil))
	if w.Code != 500 {
		t.Fatal(w.Code)
	}
	for _, configure := range []func() hostsvc.PlatformSetupManager{nil, func() hostsvc.PlatformSetupManager { return nil }} {
		m.setup = configure
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/settings/platforms/codex/setup", nil))
		if w.Code != 503 {
			t.Fatal(w.Code)
		}
	}
}
