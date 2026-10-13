package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
)

// The server mounts the maintenance module behind its own guards, and
// WithOpenCodeDBPath points the runner at the database and its dump.
func TestMaintenanceModuleWiring(t *testing.T) {
	get := func(srv *Server, method, path string) *httptest.ResponseRecorder {
		mux := http.NewServeMux()
		srv.mountModules(mux)
		req := httptest.NewRequest(method, path, nil)
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := get(&Server{}, http.MethodPost, "/api/maintenance/opencode-db/cleanup"); rec.Code != http.StatusNotFound {
		t.Errorf("without a DB path: %d, want 404", rec.Code)
	}
	path := filepath.Join(t.TempDir(), "opencode.db")
	srv := (&Server{}).WithOpenCodeDBPath(path)
	rec := get(srv, http.MethodGet, "/api/maintenance/opencode-db")
	var st struct {
		Available bool   `json:"available"`
		DumpPath  string `json:"dumpPath"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil || !st.Available || st.DumpPath != path+".ocman-diffs" {
		t.Errorf("status = %d %s, want available with the sibling dump", rec.Code, rec.Body.String())
	}
	if srv.launchBlocked() != nil {
		t.Error("an idle maintenance runner must not block launches")
	}
	if (&Server{}).WithOpenCodeDBPath("").maintMod != nil {
		t.Error("an empty DB path must leave maintenance unavailable")
	}
}

func TestMaintenanceModuleAuthAndOrigin(t *testing.T) {
	srv := &Server{auth: newTestAuth(t, "maintenance-test")}
	mux := http.NewServeMux()
	srv.mountModules(mux)
	for _, path := range []string{
		"/api/maintenance/opencode-db",
		"/api/maintenance/opencode-db/cleanup",
		"/api/maintenance/opencode-db/restore",
		"/api/maintenance/opencode-db/delete-dump",
	} {
		method, allowed := http.MethodPost, http.StatusNotFound
		if path == "/api/maintenance/opencode-db" {
			method, allowed = http.MethodGet, http.StatusOK
		}
		for _, tc := range []struct {
			name, peer, origin string
			cookie             bool
			want               int
		}{
			{"unauthenticated", "127.0.0.1:1234", "", false, http.StatusUnauthorized},
			{"local", "127.0.0.1:1234", "http://localhost:8228", true, allowed},
			{"remote", "192.0.2.1:1234", "", true, http.StatusForbidden},
			{"foreign origin", "127.0.0.1:1234", "https://evil.example", true, http.StatusForbidden},
		} {
			t.Run(path+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(method, "http://localhost:8228"+path, nil)
				req.RemoteAddr = tc.peer
				req.Header.Set("Origin", tc.origin)
				if tc.cookie {
					req.AddCookie(&http.Cookie{Name: authCookieName, Value: srv.auth.signToken(time.Now().Add(time.Hour))})
				}
				want := tc.want
				if method == http.MethodGet && tc.cookie {
					want = http.StatusOK // status requires auth, not localhost
				}
				rec := httptest.NewRecorder()
				mux.ServeHTTP(rec, req)
				if rec.Code != want {
					t.Errorf("got %d %s, want %d", rec.Code, rec.Body.String(), want)
				}
			})
		}
	}
}

type countingRuntime struct {
	fakeRuntime
	launches int
}

func (c *countingRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	c.launches++
	return c.fakeRuntime.Launch(ctx, spec)
}

func TestGatedRuntimeRefusesLaunchWhileBlocked(t *testing.T) {
	inner := &countingRuntime{}
	var blocked error
	rt := gatedRuntime{Runtime: inner, blocked: func() error { return blocked }}

	blocked = errors.New("maintenance")
	if _, err := rt.Launch(context.Background(), ocruntime.LaunchSpec{}); err == nil || inner.launches != 0 {
		t.Fatalf("blocked launch: err %v, launches %d; want refused", err, inner.launches)
	}
	if err := rt.Stop(context.Background(), &ocruntime.Instance{}); err != nil {
		t.Errorf("stop must pass through while blocked: %v", err)
	}
	blocked = nil
	if _, err := rt.Launch(context.Background(), ocruntime.LaunchSpec{}); err != nil || inner.launches != 1 {
		t.Errorf("open launch: err %v, launches %d; want launched", err, inner.launches)
	}
}

func TestGateLaunchTmuxPassesThroughWithoutMaintenance(t *testing.T) {
	srv := &Server{}
	name, err := srv.gateLaunchTmux(func(context.Context, string) (string, error) { return "s", nil })(context.Background(), "/d")
	if err != nil || name != "s" {
		t.Errorf("got %q, %v; want pass-through", name, err)
	}
	if _, ok := srv.gatedRuntime().(gatedRuntime); !ok {
		t.Error("gatedRuntime should wrap even a nil runtime")
	}
}
