package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
)

// The auto-approve module's routes are mounted through the server: they
// require a login, resolve an explicit ?platform= fail-closed, and read the
// server-wide default at request time.
func TestAutoApproveModuleRoutesThroughServer(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{id: "opencode", sessions: []db.Session{{ID: "ses-1", Platform: "opencode"}}}
	reg.Register(fp)
	reg.RememberSessions("opencode", fp.sessions)
	get := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		serveRoutes(t, srv, rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	srv.autoapproveModule() // built before the option below
	srv.WithAutoApproveDefault(true)
	if rec := get("/api/session/ses-1/auto-approve"); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"enabled":true,"overridden":false`) {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/session/ses-1/auto-approve?platform=r-gone:opencode"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown owner: %d %s", rec.Code, rec.Body)
	}
	if rec := get("/api/session/bad%20id/approved-permissions"); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid id: %d %s", rec.Code, rec.Body)
	}
	for _, path := range []string{"/api/settings/judge-delay", "/api/settings/judge-endpoint", "/api/settings/prompt-sections"} {
		if rec := get(path); rec.Code != 200 {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
	}

	srv.auth = newTestAuth(t, "hunter2")
	for _, path := range []string{"/api/session/ses-1/auto-approve", "/api/session/ses-1/approved-permissions", "/api/settings/judge-model", "/api/settings/judge-model/options"} {
		if rec := get(path); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without login: %d", path, rec.Code)
		}
	}
}
