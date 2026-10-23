package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSessionRoutes_UnknownPathReturns404(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rr := httptest.NewRecorder()
		serveRoutes(t, srv, rr, httptest.NewRequest(method, "/api/session/abc/totally-fake-subpath", nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s status = %d, want 404; body=%s", method, rr.Code, rr.Body)
		}
	}
}

// A path that matches a known pattern with the wrong method is 405 with
// the allowed methods, whether the route is the server's or a module's.
func TestRoutes_WrongMethodReturns405(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	cases := []struct{ method, path, allow string }{
		{http.MethodDelete, "/api/session/abc/agents", "GET, HEAD"},
		{http.MethodPost, "/api/projects", "GET, HEAD"},
		{http.MethodGet, "/api/client-activity", "POST"},
		{http.MethodGet, "/api/sessions/prepare", "POST"},
		{http.MethodPost, "/api/session/abc/queue", "GET, HEAD"},
		{http.MethodGet, "/api/session/abc/abort", "POST"},
		{http.MethodGet, "/api/debug/log", "POST"},
		{http.MethodPut, "/api/session/abc", "GET, HEAD, PATCH"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, httptest.NewRequest(c.method, c.path, nil))
			if rr.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405; body=%s", rr.Code, rr.Body)
			}
			if got := rr.Header().Get("Allow"); got != c.allow {
				t.Fatalf("Allow = %q, want %q", got, c.allow)
			}
		})
	}
}

// The SPA fallback serves every non-API path for GET and HEAD, without
// swallowing API 404s.
func TestRoutes_SPAFallback(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/", http.StatusOK},
		{http.MethodHead, "/", http.StatusOK},
		{http.MethodGet, "/sessions/abc", http.StatusOK},
		{http.MethodHead, "/sessions/abc", http.StatusOK},
		{http.MethodGet, "/api/no-such-route", http.StatusNotFound},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, httptest.NewRequest(c.method, c.path, nil))
			if rr.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, c.want, rr.Body)
			}
			if c.want == http.StatusOK && !strings.Contains(rr.Header().Get("Content-Type"), "text/html") {
				t.Fatalf("Content-Type = %q, want the SPA's index.html", rr.Header().Get("Content-Type"))
			}
		})
	}
}

// HEAD on a GET route reaches the handler, as GET patterns match HEAD.
func TestRoutes_HeadReachesGETRoute(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	rr := httptest.NewRecorder()
	serveRoutes(t, srv, rr, httptest.NewRequest(http.MethodHead, "/api/projects", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
}
