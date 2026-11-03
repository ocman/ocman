package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/remote"
)

func TestPublishedResourcesDisconnected(t *testing.T) {
	serve := filesystemMux(t, testServer(t))
	for _, path := range []string{"api/file/token", "api/artifacts/report", "api/artifacts/report/files/0", "api/artifacts"} {
		rr := serve(httptest.NewRequest(http.MethodGet, "/api/remote-resources/missing/"+path, nil))
		if rr.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, rr.Code)
		}
	}
}

func TestPublishedResourcesRequireHubLogin(t *testing.T) {
	srv := testServer(t)
	srv.auth = &Auth{hmacKey: []byte(strings.Repeat("k", 32)), cookieName: authCookieName}
	serve := filesystemMux(t, srv)
	if rr := serve(httptest.NewRequest(http.MethodGet, "/api/remote-resources/missing/api/file/token", nil)); rr.Code != 401 {
		t.Fatalf("unauthenticated resource = %d", rr.Code)
	}
}

func TestPublishedResourcesRemoteRoundTrip(t *testing.T) {
	owner, _, create := artifactWiring(t)
	created := create(map[string]any{
		"action": "create", "title": "Remote report", "directory": "/repo",
		"platform": "opencode", "session_id": "s1",
		"files": []any{map[string]any{"name": "report.html", "content": "<h1>remote</h1>"}},
	})
	id := created["id"].(string)
	file := filepath.Join(t.TempDir(), "large.txt")
	content := strings.Repeat("remote", 800_000)
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	fileURL, err := owner.filesystemModule().FileURL(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	hub := testServer(t)
	hub.hostRouter = hostsvc.NewRouter(identityRPCHost{})
	rpc := remote.NewServer(owner.registry, identityRPCHost{}, "owner", "test").UsePublishedResources(owner.ServePublishedResource)
	listener, err := remote.NewListener(remote.ListenConfig{Addr: "127.0.0.1:0", Token: "token", TrustedOverlay: true}, rpc)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = listener.Serve() }()
	t.Cleanup(listener.Stop)
	manager := remote.NewManager(hub.registry, hub.hostRouter, hub.stateDB, "opencode")
	t.Cleanup(manager.Stop)
	if _, err := manager.Add(t.Context(), "grpc://"+listener.Addr(), "token", "owner"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := hub.hostRouter.LookupRemote("owner"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("remote did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	serve := filesystemMux(t, hub)
	get := func(path string) *httptest.ResponseRecorder {
		return serve(httptest.NewRequest(http.MethodGet, "/api/remote-resources/owner"+path, nil))
	}
	for _, tc := range []struct{ path, body string }{
		{fileURL, content}, {"/api/artifacts/" + id + "/files/0", "<h1>remote</h1>"},
	} {
		rr := get(tc.path)
		if rr.Code != 200 || rr.Body.String() != tc.body {
			t.Fatalf("%s: code=%d bytes=%d", tc.path, rr.Code, rr.Body.Len())
		}
		if rr.Header().Get("Content-Security-Policy") == "" || rr.Header().Get("X-Content-Type-Options") != "nosniff" || rr.Header().Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatalf("missing safety headers: %v", rr.Header())
		}
		req := httptest.NewRequest(http.MethodGet, "/api/remote-resources/owner"+tc.path, nil)
		req.Header.Set("Range", "bytes=0-4")
		rr = serve(req)
		if rr.Code != 206 || rr.Body.String() != tc.body[:5] {
			t.Fatalf("range = %d %q", rr.Code, rr.Body.String())
		}
		rr = serve(httptest.NewRequest(http.MethodHead, "/api/remote-resources/owner"+tc.path, nil))
		if rr.Code != 200 || rr.Body.Len() != 0 {
			t.Fatalf("HEAD = %d bytes=%d", rr.Code, rr.Body.Len())
		}
	}
	if rr := get("/api/artifacts/" + id); rr.Code != 200 || !strings.Contains(rr.Body.String(), "Remote report") {
		t.Fatalf("metadata = %d %s", rr.Code, rr.Body)
	}
	if rr := get("/api/artifacts?directory=/repo"); rr.Code != 200 || !strings.Contains(rr.Body.String(), id) {
		t.Fatalf("list = %d %s", rr.Code, rr.Body)
	}
	if rr := get("/api/artifacts?platform=r-owner:opencode&sessionId=s1"); rr.Code != 200 || !strings.Contains(rr.Body.String(), id) {
		t.Fatalf("owner-local session filter = %d %s", rr.Code, rr.Body)
	}
	if rr := get("/api/artifacts/" + id + "/files/0/interactive"); rr.Code != 200 || !strings.Contains(rr.Header().Get("Content-Security-Policy"), "sandbox allow-scripts;") {
		t.Fatalf("interactive = %d %v", rr.Code, rr.Header())
	}
	if rr := get("/api/file/invalid"); rr.Code != 403 {
		t.Fatalf("invalid token = %d", rr.Code)
	}
	if rr := get("/api/artifacts/missing/files/0"); rr.Code != 404 {
		t.Fatalf("missing artifact = %d", rr.Code)
	}
	body, _ := json.Marshal(map[string]string{"url": fileURL})
	rr := serve(httptest.NewRequest(http.MethodPost, "/api/remote-resources/owner/api/files/refresh", strings.NewReader(string(body))))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "/api/file/") {
		t.Fatalf("refresh = %d %s", rr.Code, rr.Body)
	}
	// The hub cannot validate the remote's signature, even with the same path.
	if rr := serve(httptest.NewRequest(http.MethodGet, fileURL, nil)); rr.Code != 403 {
		t.Fatalf("hub token = %d", rr.Code)
	}
	for _, path := range []string{"/api/settings", "/api/artifacts/" + id + "/share"} {
		if rr := get(path); rr.Code != 404 {
			t.Fatalf("non-resource %s = %d", path, rr.Code)
		}
	}
	if rr := serve(httptest.NewRequest(http.MethodDelete, "/api/remote-resources/owner/api/artifacts/"+id, nil)); rr.Code != 404 {
		t.Fatalf("mutation = %d", rr.Code)
	}
}
