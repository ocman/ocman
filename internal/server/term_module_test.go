package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// serveTerm sends req through the terminal module's routes as a loopback,
// origin-less client, so the server's real guards and owner resolution run.
func serveTerm(s *Server, w http.ResponseWriter, req *http.Request) {
	mux := http.NewServeMux()
	s.termModule().Routes(moduleRouter{s: s, mux: mux})
	req.RemoteAddr = "127.0.0.1:12345"
	mux.ServeHTTP(w, req)
}

// The terminal routes are mounted from the module registry behind the
// localhost guard: a non-loopback peer or a cross-origin browser never
// reaches validation, owner resolution or the upgrade.
func TestTermRoutesMountedBehindGuards(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		method, path, remote, origin string
	}{
		{http.MethodGet, "/api/term/ws?dir=/repo", "192.0.2.1:1", ""},
		{http.MethodGet, "/api/term/windows?dir=/repo", "192.0.2.1:1", ""},
		{http.MethodPost, "/api/term/windows", "192.0.2.1:1", ""},
		{http.MethodDelete, "/api/term/windows", "192.0.2.1:1", ""},
		{http.MethodGet, "/api/term/ws?dir=/repo", "127.0.0.1:1", "https://evil.example"},
		{http.MethodPost, "/api/term/windows", "127.0.0.1:1", "https://evil.example"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path+" from "+tt.remote+" "+tt.origin, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(`{"dir":"/repo"}`))
			req.RemoteAddr = tt.remote
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A foreign-origin WebSocket through the real server routes is refused.
func TestTermWebSocketRejectsForeignOrigin(t *testing.T) {
	mux, err := testServer(t).routes()
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mux)
	defer srv.Close()

	header := http.Header{"Origin": []string{"https://evil.example"}}
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/api/term/ws?dir=/repo", header)
	if conn != nil {
		conn.Close()
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign-origin dial err = %v, resp = %#v; want 403", err, resp)
	}
}

func TestTermRoutesRequireAuthOnLoopback(t *testing.T) {
	s := testServer(t)
	s.auth = newTestAuth(t, "pw")
	mux, err := s.routes()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/term/windows?dir=/repo", "/api/term/ws?dir=/repo"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "127.0.0.1:12345"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403: %s", path, rec.Code, rec.Body.String())
		}
	}
}
