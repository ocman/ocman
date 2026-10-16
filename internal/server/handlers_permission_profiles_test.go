package server

import (
	"github.com/NoUseFreak/ocman/internal/pluginsmod"

	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
)

const readOnlyProfileBody = `{"name":"read-only","rules":[{"permission":"bash","pattern":"*","action":"deny"},{"permission":"edit","pattern":"*","action":"deny"},{"permission":"write","pattern":"*","action":"deny"}]}`

func permissionProfileHTTPServer(t *testing.T, fake *fakePlatform) (*Server, http.Handler) {
	t.Helper()
	srv := testServer(t)
	registry := platforms.NewRegistry()
	registry.Register(fake)
	srv.registry = registry
	srv.sessions = sessionsvc.New(registry, sessionsvc.Hooks{})
	srv.hostRouter = hostsvc.NewRouter(&routineTestHost{})
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	return srv, mux
}

func TestPermissionProfileHTTPLifecycle(t *testing.T) {
	_, handler := permissionProfileHTTPServer(t, &fakePlatform{id: "opencode"})

	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("empty list: %d %s", rec.Code, rec.Body.String())
	}

	create := doRoutineRequest(t, handler, http.MethodPost, "/api/permission-profiles", readOnlyProfileBody)
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		RulesJSON string `json:"rulesJSON"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || created.ID == "" || created.Name != "read-only" || !strings.Contains(created.RulesJSON, `"deny"`) {
		t.Fatalf("created=%+v err=%v", created, err)
	}

	// Unique names across the whole collection, case-insensitively.
	if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/permission-profiles", strings.Replace(readOnlyProfileBody, "read-only", "Read-Only", 1)); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate name: %d %s", rec.Code, rec.Body.String())
	}

	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles/"+created.ID, ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"read-only"`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body.String())
	}

	updated := doRoutineRequest(t, handler, http.MethodPut, "/api/permission-profiles/"+created.ID, strings.Replace(readOnlyProfileBody, "read-only", "renamed", 1))
	if updated.Code != http.StatusOK || !strings.Contains(updated.Body.String(), `"name":"renamed"`) {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}

	if rec := doRoutineRequest(t, handler, http.MethodDelete, "/api/permission-profiles/"+created.ID, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("list after delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete: %d", rec.Code)
	}
	if rec := doRoutineRequest(t, handler, http.MethodDelete, "/api/permission-profiles/"+created.ID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("double delete: %d", rec.Code)
	}
}

func TestPermissionProfileHTTPValidation(t *testing.T) {
	_, handler := permissionProfileHTTPServer(t, &fakePlatform{id: "opencode"})
	for _, body := range []string{
		`{`,
		`{"name":"","rules":[{"permission":"bash","pattern":"*","action":"deny"}]}`,
		`{"name":"p","rules":[]}`,
		`{"name":"p","rules":[{"permission":"","pattern":"*","action":"deny"}]}`,
		`{"name":"p","rules":[{"permission":"bash","pattern":"*","action":"yolo"}]}`,
	} {
		if rec := doRoutineRequest(t, handler, http.MethodPost, "/api/permission-profiles", body); rec.Code != http.StatusBadRequest {
			t.Fatalf("body %s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles/", ""); rec.Code != http.StatusOK || rec.Body.String() != "[]\n" {
		t.Fatalf("trailing slash: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doRoutineRequest(t, handler, http.MethodGet, "/api/permission-profiles/x/y", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("nested path: %d", rec.Code)
	}
	if rec := doRoutineRequest(t, handler, http.MethodPatch, "/api/permission-profiles", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("bad method: %d", rec.Code)
	}
}

// A resolved profile hands sessionsvc.CreateConfigured exactly the pinned
// rules: the fake adapter must see the resolved set verbatim.
func TestResolvedProfileReachesCreateConfigured(t *testing.T) {
	srv, handler := permissionProfileHTTPServer(t, &fakePlatform{id: "opencode"})
	ctx := t.Context()

	var applied []platforms.PermissionRule
	var appliedSession string
	srv.registry.Register(&fakePlatform{
		id: "opencode",
		createSessionFn: func(platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			return &platforms.CreateSessionResponse{ID: "session-1"}, nil
		},
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			applied, appliedSession = req.Rules, req.SessionID
			return nil
		},
	})

	rec := doRoutineRequest(t, handler, http.MethodPost, "/api/permission-profiles", readOnlyProfileBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	resolved, err := pluginsmod.NewStore(srv.stateDB).ResolvePermissionProfile(ctx, "read-only")
	if err != nil || len(resolved) == 0 {
		t.Fatalf("resolve: %v", err)
	}

	if _, err := srv.sessions.CreateConfigured(ctx, "opencode", platforms.CreateSessionRequest{Directory: "/repo"}, resolved); err != nil {
		t.Fatal(err)
	}
	if appliedSession != "session-1" {
		t.Fatalf("session = %q", appliedSession)
	}
	if len(applied) != len(resolved) {
		t.Fatalf("applied = %+v, want %+v", applied, resolved)
	}
	for i := range resolved {
		if applied[i] != resolved[i] {
			t.Fatalf("rule %d = %+v, want %+v", i, applied[i], resolved[i])
		}
	}
}

// Profiles use normal authentication: a non-loopback client with a valid
// session cookie manages them, an unauthenticated one gets 401, and a
// cross-site write is refused even with the cookie.
func TestPermissionProfileHTTPNormalAuth(t *testing.T) {
	srv, _ := permissionProfileHTTPServer(t, &fakePlatform{id: "opencode"})
	srv.auth = newTestAuth(t, "hunter2")
	mux, err := srv.routes()
	if err != nil {
		t.Fatal(err)
	}
	cookies := httptest.NewRecorder()
	srv.auth.issueCookie(cookies, httptest.NewRequest(http.MethodGet, "/", nil))
	cookie := cookies.Result().Cookies()[0]
	do := func(method, body string, authed bool, origin string) int {
		t.Helper()
		r := httptest.NewRequest(method, "http://ocman.example:8228/api/permission-profiles", strings.NewReader(body))
		r.RemoteAddr = "10.0.0.5:1234"
		if authed {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	if code := do(http.MethodGet, "", false, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated list = %d, want 401", code)
	}
	if code := do(http.MethodPost, readOnlyProfileBody, false, ""); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated create = %d, want 401", code)
	}
	if code := do(http.MethodPost, readOnlyProfileBody, true, "http://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-site create = %d, want 403", code)
	}
	if code := do(http.MethodPost, readOnlyProfileBody, true, "http://ocman.example:8228"); code != http.StatusCreated {
		t.Fatalf("authenticated remote create = %d, want 201", code)
	}
	if code := do(http.MethodGet, "", true, ""); code != http.StatusOK {
		t.Fatalf("authenticated remote list = %d, want 200", code)
	}
}
