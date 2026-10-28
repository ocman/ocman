package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
)

// serveProjectForge sends r through the public mux as an origin-less
// loopback client, so the project forge module's real guards and wiring
// (owner resolution, forge clients, registry) run.
func serveProjectForge(srv *Server, w http.ResponseWriter, r *http.Request) {
	mux, err := srv.routes()
	if err != nil {
		panic(err)
	}
	r.RemoteAddr = "127.0.0.1:1234"
	mux.ServeHTTP(w, r)
}

func projectForgeRoute(srv *Server) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { serveProjectForge(srv, w, r) }
}

// A named owner that is not connected never degrades to the hub: every
// project forge route fails closed before detecting or launching anything.
func TestProjectForgeRoutesRejectDisconnectedOwner(t *testing.T) {
	srv := testServer(t)
	local := &projectHandleRemoteHost{upstreams: githubProjectUpstreams("/repo"), remoteID: "local"}
	srv.hostRouter = hostsvc.NewRouter(local)
	const q = "?dir=/repo&remoteId=gone&remote=origin&sha=abc123&number=42"
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/project/upstreams"+q, nil),
		httptest.NewRequest(http.MethodGet, "/api/project/prs"+q, nil),
		httptest.NewRequest(http.MethodGet, "/api/project/issues"+q, nil),
		httptest.NewRequest(http.MethodGet, "/api/project/pr-checks"+q, nil),
		httptest.NewRequest(http.MethodGet, "/api/project/pr-mergeability"+q, nil),
		httptest.NewRequest(http.MethodGet, "/api/project/forge-user"+q, nil),
		httptest.NewRequest(http.MethodPost, "/api/project/handle", bytes.NewBufferString(
			`{"dir":"/repo","remoteId":"gone","remote":"origin","type":"issue","number":7,"mode":"session"}`)),
	} {
		t.Run(req.URL.Path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			serveProjectForge(srv, rr, req)
			if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), `"code":"remote_not_connected"`) {
				t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
			}
		})
	}
	if local.upstreamCalls != 0 {
		t.Fatalf("hub detected upstreams %d times for a disconnected owner", local.upstreamCalls)
	}
}

// Method is checked before the guard, as the server's method patterns do.
func TestProjectForgeRoutesRejectWrongMethod(t *testing.T) {
	srv := testServer(t)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "/api/project/upstreams", nil),
		httptest.NewRequest(http.MethodPost, "/api/project/prs", nil),
		httptest.NewRequest(http.MethodGet, "/api/project/handle", nil),
	} {
		req.RemoteAddr = "192.0.2.1:1234"
		rr := httptest.NewRecorder()
		mux, err := srv.routes()
		if err != nil {
			t.Fatal(err)
		}
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s %s: status = %d", req.Method, req.URL.Path, rr.Code)
		}
	}
}

func TestPromptTemplatesRouteRequiresAuthAndBroadcasts(t *testing.T) {
	srv := testServer(t)
	srv.auth = newTestAuth(t, "hunter2")
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	rr := httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(`{"pr":"x"}`)))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated save: status = %d", rr.Code)
	}

	srv.auth = nil
	rr = httptest.NewRecorder()
	serveProjectForge(srv, rr, httptest.NewRequest(http.MethodPost, "/api/settings/prompt-templates", bytes.NewBufferString(`{"pr":"x"}`)))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"pr":"x"`) {
		t.Fatalf("save: status = %d, body = %s", rr.Code, rr.Body.String())
	}
	select {
	case event := <-sub.ch:
		if event.event != "ocman.settings.changed" {
			t.Fatalf("event = %+v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("saving templates did not invalidate other clients' settings")
	}
}
