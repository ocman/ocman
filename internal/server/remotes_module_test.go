package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
	opencodeplatform "github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
	"github.com/NoUseFreak/ocman/internal/remote"
)

func withManager(t *testing.T, srv *Server) *remote.Manager {
	t.Helper()
	mgr := remote.NewManager(srv.Registry(), srv.HostRouter(), srv.stateDB, string(opencodeplatform.PlatformID))
	srv.SetRemoteManager(mgr)
	t.Cleanup(mgr.Stop)
	return mgr
}

func serveMux(t *testing.T, mux http.Handler, method, path, body, peer string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if peer != "" {
		req.RemoteAddr = peer
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

func TestRevealRemoteTokenRouteRejectsNonLoopback(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	if rr := serveMux(t, mux, http.MethodPost, "/api/settings/remote-access/reveal-token", "", "192.0.2.1:1234"); rr.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403", rr.Code)
	}
	if rr := serveMux(t, mux, http.MethodGet, "/api/settings/remote-access/reveal-token", "", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET status %d, want 405", rr.Code)
	}
}

// The mounted routes see options and the manager applied after the mux is
// built, as main.go does, and never echo a token.
func TestRemotesModuleReadsServerStateAtCallTime(t *testing.T) {
	srv := testServer(t)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	if rr := serveMux(t, mux, http.MethodPost, "/api/remotes", `{"address":"127.0.0.1:59999","token":"t"}`, "127.0.0.1:1234"); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("no manager = %d, want 503", rr.Code)
	}
	if rr := serveMux(t, mux, http.MethodGet, "/api/remotes", "", "127.0.0.1:1234"); rr.Code != 200 || rr.Body.String() != "[]\n" {
		t.Fatalf("no manager list = %d %q", rr.Code, rr.Body)
	}

	srv.WithRemoteAccess("inst123", "0.0.0.0:8230", true, true)
	withManager(t, srv)

	rr := serveMux(t, mux, http.MethodGet, "/api/settings/remote-access", "", "")
	var access map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &access); err != nil {
		t.Fatal(err)
	}
	if access["instanceId"] != "inst123" || access["transport"] != "tls" || access["tokenSet"] != true {
		t.Fatalf("access %v", access)
	}
	ident, _ := srv.stateDB.InstanceIdentity(t.Context())
	if bytes.Contains(rr.Body.Bytes(), []byte(ident.RemoteToken)) {
		t.Fatal("remote-access status leaked the token")
	}

	rr = serveMux(t, mux, http.MethodPost, "/api/remotes", `{"address":"127.0.0.1:59999","token":"hub-secret","displayName":"Box"}`, "127.0.0.1:1234")
	if rr.Code != 200 || bytes.Contains(rr.Body.Bytes(), []byte("hub-secret")) {
		t.Fatalf("add = %d %s", rr.Code, rr.Body)
	}
	rr = serveMux(t, mux, http.MethodGet, "/api/remotes", "", "127.0.0.1:1234")
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"displayName":"Box"`)) || bytes.Contains(rr.Body.Bytes(), []byte("hub-secret")) {
		t.Fatalf("list %s", rr.Body)
	}
}

func TestRemotesRoutesRequireAuth(t *testing.T) {
	srv := testServer(t)
	srv.auth = newTestAuth(t, "pw")
	withManager(t, srv)
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/settings/remote-access"},
		{http.MethodPost, "/api/settings/remote-access/reveal-token"},
		{http.MethodGet, "/api/remotes"},
		{http.MethodPost, "/api/remotes/1/reconnect"},
	} {
		if rr := serveMux(t, mux, c.method, c.path, "", ""); rr.Code != http.StatusUnauthorized {
			t.Errorf("%s %s = %d, want 401", c.method, c.path, rr.Code)
		}
	}
}

// The inventory loop runs among the server's workers: Start does not return
// until it has, so it cannot outlive the databases closed after Start.
func TestRemotesModuleRunsAmongJoinedWorkers(t *testing.T) {
	srv := testServer(t)
	withManager(t, srv)
	var runner bool
	for _, m := range srv.modules() {
		if m.Name() == "remotes" {
			_, runner = m.(interface{ Run(context.Context) })
		}
	}
	if !runner {
		t.Fatal("remotes module is not a Runner")
	}
	ctx, cancel := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	if err := startModules(ctx, []module.Module{srv.remotesModule()}, &workers); err != nil {
		t.Fatal(err)
	}
	cancel()
	workers.Wait()
}
