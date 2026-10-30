package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/git"
	"github.com/ocman/ocman/internal/httpx"
)

// filesystemMux serves requests through the server's real route table, so
// the filesystem module's guards and owner wiring are the server's.
func filesystemMux(t *testing.T, srv *Server) func(*http.Request) *httptest.ResponseRecorder {
	t.Helper()
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	return func(req *http.Request) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		withSecurityHeaders(mux).ServeHTTP(rec, req)
		return rec
	}
}

func TestRepoFilesRoutes(t *testing.T) {
	srv := testServer(t)
	serve := filesystemMux(t, srv)
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	get := func(path string, kv ...string) *httptest.ResponseRecorder {
		v := url.Values{}
		for i := 0; i < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		return serve(httptest.NewRequest(http.MethodGet, path+"?"+v.Encode(), nil))
	}

	rr := get("/api/git/files", "dir", dir, "remoteId", "local")
	var list git.FileList
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &list) != nil {
		t.Fatalf("list status = %d, body = %s", rr.Code, rr.Body.String())
	}
	// .env is untracked and not ignored here, so it is listed.
	if len(list.Files) != 2 {
		t.Fatalf("files = %v", list.Files)
	}

	rr = get("/api/git/file", "dir", dir, "remoteId", "local", "path", "foo.txt")
	var file git.FileContent
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &file) != nil || file.Content != "hello\n" {
		t.Fatalf("file status = %d, body = %s", rr.Code, rr.Body.String())
	}

	// ignored=1 lists and reads gitignored files; without it they are 404.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.log"), []byte("log"), 0o644); err != nil {
		t.Fatal(err)
	}
	rr = get("/api/git/files", "dir", dir, "remoteId", "local", "ignored", "1")
	if rr.Code != http.StatusOK || json.Unmarshal(rr.Body.Bytes(), &list) != nil || list.Files[len(list.Files)-1] != "x.log" {
		t.Fatalf("ignored list status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if rr = get("/api/git/file", "dir", dir, "remoteId", "local", "path", "x.log", "ignored", "1"); rr.Code != http.StatusOK {
		t.Fatalf("ignored read status = %d", rr.Code)
	}
	if rr = get("/api/git/file", "dir", dir, "remoteId", "local", "path", "x.log"); rr.Code != http.StatusNotFound {
		t.Fatalf("hidden read status = %d", rr.Code)
	}

	cases := []struct {
		name, path string
		kv         []string
		want       int
	}{
		{"missing remoteId", "/api/git/files", []string{"dir", dir}, http.StatusBadRequest},
		{"missing dir", "/api/git/files", []string{"remoteId", "local"}, http.StatusBadRequest},
		{"relative dir", "/api/git/files", []string{"dir", "rel", "remoteId", "local"}, http.StatusBadRequest},
		{"not a repo", "/api/git/files", []string{"dir", t.TempDir(), "remoteId", "local"}, http.StatusNotFound},
		{"missing path", "/api/git/file", []string{"dir", dir, "remoteId", "local"}, http.StatusBadRequest},
		{"escape", "/api/git/file", []string{"dir", dir, "remoteId", "local", "path", "../x"}, http.StatusNotFound},
		{"absolute escape", "/api/git/file", []string{"dir", dir, "remoteId", "local", "path", "/etc/hosts"}, http.StatusNotFound},
	}
	for _, c := range cases {
		if rr := get(c.path, c.kv...); rr.Code != c.want {
			t.Errorf("%s: status = %d, want %d (%s)", c.name, rr.Code, c.want, rr.Body.String())
		}
	}

	for _, path := range []string{"/api/git/files", "/api/git/file"} {
		rr := serve(httptest.NewRequest(http.MethodPost, path+"?dir="+url.QueryEscape(dir)+"&remoteId=local&path=foo.txt", nil))
		if rr.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: status = %d, want 405", path, rr.Code)
		}
	}
}

// An explicit remote that is not connected fails closed instead of
// reading the hub's checkout of the same path.
func TestRepoFilesRoutes_DisconnectedOwnerFailsClosed(t *testing.T) {
	serve := filesystemMux(t, testServer(t))
	dir := t.TempDir()
	gitInitForServerTest(t, dir)
	for _, path := range []string{"/api/git/files", "/api/git/file"} {
		rr := serve(httptest.NewRequest(http.MethodGet, path+"?remoteId=gone&path=foo.txt&dir="+url.QueryEscape(dir), nil))
		if rr.Code != http.StatusServiceUnavailable || !strings.Contains(rr.Body.String(), "remote_not_connected") {
			t.Errorf("%s: status = %d, body = %s; want 503 remote_not_connected", path, rr.Code, rr.Body.String())
		}
	}
}

// The directory picker routes are GET-only and localhost-only; the guards
// reject before the handler touches the filesystem.
func TestFilesystemRoutes_MethodAndLocalhostEnforcement(t *testing.T) {
	serve := filesystemMux(t, testServer(t))
	tests := []struct {
		name       string
		method     string
		remoteAddr string
		origin     string
		wantCode   int
	}{
		{"get from loopback", http.MethodGet, "127.0.0.1:1234", "", http.StatusOK},
		{"post rejected", http.MethodPost, "127.0.0.1:1234", "", http.StatusMethodNotAllowed},
		{"delete rejected", http.MethodDelete, "127.0.0.1:1234", "", http.StatusMethodNotAllowed},
		{"remote peer rejected", http.MethodGet, "10.1.2.3:1234", "", http.StatusForbidden},
		{"foreign origin rejected", http.MethodGet, "127.0.0.1:1234", "https://evil.example", http.StatusForbidden},
	}
	for _, path := range []string{"/api/filesystem/directories", "/api/filesystem/directory-search"} {
		for _, tt := range tests {
			t.Run(path+"/"+tt.name, func(t *testing.T) {
				req := httptest.NewRequest(tt.method, path+"?dir="+url.QueryEscape(t.TempDir())+"&root="+url.QueryEscape(t.TempDir())+"&q=", nil)
				req.RemoteAddr = tt.remoteAddr
				req.Host = "localhost:8228"
				if tt.origin != "" {
					req.Header.Set("Origin", tt.origin)
				}
				if rr := serve(req); rr.Code != tt.wantCode {
					t.Fatalf("got %d, want %d: %s", rr.Code, tt.wantCode, rr.Body.String())
				}
			})
		}
	}
}

// FileURL (the embed_file tool's signer) mints a link the server's own
// /api/file/ route serves inert, framable only by the dashboard, behind
// the login when auth is on.
func TestFileRoute_ServesSignedURLInertBehindAuth(t *testing.T) {
	srv := testServer(t)
	serve := filesystemMux(t, srv)
	svg := filepath.Join(t.TempDir(), "x.svg")
	if err := os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	link, err := srv.FileURL(t.Context(), svg)
	if err != nil || !strings.HasPrefix(link, "/api/file/") {
		t.Fatalf("FileURL = %q, %v", link, err)
	}

	rr := serve(httptest.NewRequest(http.MethodGet, link, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rr.Code, rr.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": httpx.InertFileCSP,
		"X-Frame-Options":         "SAMEORIGIN",
		"X-Content-Type-Options":  "nosniff",
	} {
		if got := rr.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if ct := rr.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q", ct)
	}

	if rr := serve(httptest.NewRequest(http.MethodPost, link, nil)); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", rr.Code)
	}
	tampered := link[:len(link)-2] + "AA"
	if rr := serve(httptest.NewRequest(http.MethodGet, tampered, nil)); rr.Code != http.StatusForbidden {
		t.Errorf("tampered token status = %d, want 403", rr.Code)
	}

	srv.auth = &Auth{hmacKey: []byte(strings.Repeat("k", 32)), cookieName: authCookieName}
	for _, path := range []string{link, "/api/git/files?remoteId=local&dir=/tmp", "/api/git/file?remoteId=local&dir=/tmp&path=a"} {
		if rr := serve(httptest.NewRequest(http.MethodGet, path, nil)); rr.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated %s status = %d, want 401", path, rr.Code)
		}
	}
}
