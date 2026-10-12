package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcptest"

	"github.com/NoUseFreak/ocman/internal/artifacts"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/testutil"
)

type artifactHost struct{ hostsvc.Host }

func (artifactHost) RemoteID() string { return "local" }

func (artifactHost) Projects(context.Context) ([]db.ProjectStats, error) {
	return []db.ProjectStats{{Directory: "/repo"}}, nil
}

func (artifactHost) ProjectUpstreams(context.Context, string) (*hostsvc.ProjectUpstreams, error) {
	return &hostsvc.ProjectUpstreams{RepoRoot: "/repo"}, nil
}

// artifactWiring serves /api/artifacts through the server's routes and
// calls the module's MCP tool, so auth, signing and URLs are the server's.
func artifactWiring(t *testing.T) (*Server, func(*http.Request) *httptest.ResponseRecorder, func(map[string]any) map[string]any) {
	t.Helper()
	srv := testServer(t)
	srv.hostRouter = hostsvc.NewRouter(artifactHost{})
	srv.auth = &Auth{hmacKey: []byte(strings.Repeat("k", 32)), cookieName: authCookieName}
	srv.publicBaseURL = "https://ocman.example"
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	tools, err := mcptest.NewServer(t, srv.artifactsModule().Tools()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tools.Close)
	serve := func(req *http.Request) *httptest.ResponseRecorder {
		req.RemoteAddr = "127.0.0.1:1234"
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	call := func(args map[string]any) map[string]any {
		t.Helper()
		var out map[string]any
		text := testutil.ResultText(testutil.CallTool(t, tools, "artifacts", args))
		if err := json.Unmarshal([]byte(text), &out); err != nil {
			t.Fatalf("tool result %q: %v", text, err)
		}
		return out
	}
	return srv, serve, call
}

func itemURL(t *testing.T, view map[string]any, i int) string {
	t.Helper()
	items, _ := view["items"].([]any)
	item, _ := items[i].(map[string]any)
	u, _ := item["url"].(string)
	return u
}

func TestArtifactMCPSignedURLs(t *testing.T) {
	srv, serve, call := artifactWiring(t)
	created := call(map[string]any{
		"action": "create", "title": "Report", "directory": "/repo",
		"files": []any{map[string]any{"name": "a.md", "content": "# hi"}, map[string]any{"name": "b.md", "content": "secret"}},
		"links": []any{map[string]any{"url": "https://example.com"}},
	})
	id, _ := created["id"].(string)
	if created["url"] != "https://ocman.example/artifacts/"+id {
		t.Fatalf("page url = %v", created["url"])
	}
	got := call(map[string]any{"action": "get", "artifact_id": id})
	for _, view := range []map[string]any{created, got} {
		u, err := url.Parse(itemURL(t, view, 0))
		if err != nil || u.Host != "ocman.example" || u.Query().Get("token") == "" {
			t.Fatalf("expected absolute signed URL, got %q", itemURL(t, view, 0))
		}
		rec := serve(httptest.NewRequest(http.MethodGet, u.RequestURI(), nil))
		if rec.Code != http.StatusOK || rec.Body.String() != "# hi" {
			t.Fatalf("signed fetch = %d, %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "private, no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("signed response headers = %v", rec.Header())
		}
		if itemURL(t, view, 2) != "https://example.com" {
			t.Fatal("external link changed")
		}
	}
	u, _ := url.Parse(itemURL(t, got, 0))
	for name, target := range map[string]string{
		"unsigned":         artifacts.FilePath(id, 0),
		"tampered token":   u.RequestURI() + "x",
		"other file":       artifacts.FilePath(id, 1) + "?" + u.RawQuery,
		"other artifact":   artifacts.FilePath("other", 0) + "?" + u.RawQuery,
		"metadata":         "/api/artifacts/" + id + "?" + u.RawQuery,
		"list":             "/api/artifacts?" + u.RawQuery,
		"interactive":      u.Path + "/interactive?" + u.RawQuery,
		"expired":          u.Path + "?token=" + srv.auth.artifactURLToken(u.Path, time.Now().Add(-time.Second)),
		"cookie signature": u.Path + "?token=" + srv.auth.signToken(time.Now().Add(time.Minute)),
	} {
		if rec := serve(httptest.NewRequest(http.MethodGet, target, nil)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, rec.Code)
		}
	}
	if rec := serve(httptest.NewRequest(http.MethodPost, u.RequestURI(), nil)); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d", rec.Code)
	}
	for _, target := range []string{u.Path, u.Path + "?token=invalid"} {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: authCookieName, Value: srv.auth.signToken(time.Now().Add(time.Hour))})
		if rec := serve(req); rec.Code != http.StatusOK {
			t.Fatalf("browser cookie fetch = %d", rec.Code)
		}
	}
	stored, err := srv.stateDB.GetArtifact(t.Context(), id)
	if err != nil || stored.Items[0].URL != "" {
		t.Fatalf("signed URL persisted: %v, %v", stored, err)
	}
	// Deleting is localhost-only, through the server's own guard.
	req := httptest.NewRequest(http.MethodDelete, "/api/artifacts/"+id, nil)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: srv.auth.signToken(time.Now().Add(time.Hour))})
	remote := httptest.NewRecorder()
	req.RemoteAddr = "10.0.0.1:1"
	srv.artifactsModule().ServeHTTP(remote, req)
	if remote.Code != http.StatusForbidden {
		t.Fatalf("non-local delete = %d", remote.Code)
	}
	if err := srv.stateDB.DeleteArtifact(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	if rec := serve(httptest.NewRequest(http.MethodGet, u.RequestURI(), nil)); rec.Code != http.StatusNotFound {
		t.Fatalf("deleted artifact = %d", rec.Code)
	}
}

func TestArtifactURLTokenScopeAndExpiry(t *testing.T) {
	a := &Auth{hmacKey: []byte(strings.Repeat("k", 32))}
	now := time.Unix(1234567890, 0)
	path := artifacts.FilePath("one", 0)
	token := a.artifactURLToken(path, now.Add(artifactURLTTL))
	for _, tc := range []struct {
		name, path, token string
		now               time.Time
		want              bool
	}{
		{"valid", path, token, now, true},
		{"just before expiry", path, token, now.Add(artifactURLTTL - time.Second), true},
		{"expired", path, token, now.Add(artifactURLTTL), false},
		{"too far in future", path, a.artifactURLToken(path, now.Add(artifactURLTTL+time.Second)), now, false},
		{"other path", artifacts.FilePath("one", 1), token, now, false},
		{"cookie token", path, a.signToken(now.Add(artifactURLTTL)), now, false},
		{"malformed", path, "bad.signature", now, false},
		{"no signature", path, "1234567900", now, false},
		{"tampered", path, token + "x", now, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := a.validArtifactURLToken(tc.path, tc.token, tc.now); got != tc.want {
				t.Fatalf("valid = %v, want %v", got, tc.want)
			}
		})
	}
	other := &Auth{hmacKey: []byte(strings.Repeat("x", 32))}
	if other.validArtifactURLToken(path, token, now) {
		t.Fatal("different signing key accepted")
	}
}
