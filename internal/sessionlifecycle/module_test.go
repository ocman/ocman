package sessionlifecycle

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/module"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

type routeTestRouter struct {
	module.Router
	mux *http.ServeMux
}

func (r routeTestRouter) Handle(pattern string, h http.HandlerFunc) { r.mux.HandleFunc(pattern, h) }
func (r routeTestRouter) Local(pattern string, _ http.HandlerFunc) {
	r.mux.HandleFunc(pattern, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "local guard", http.StatusForbidden)
	})
}

var directRoutes = []string{
	"POST /api/session/s1/attachment",
	"POST /api/session/s1/restart-opencode",
	"POST /api/session/s1/reload-opencode",
	"POST /api/session/s1/command",
	"POST /api/session/s1/shell",
	"POST /api/session/s1/abort",
	"POST /api/session/s1/revert",
	"POST /api/session/s1/unrevert",
	"POST /api/session/s1/compact",
	"POST /api/session/s1/fork",
	"POST /api/session/s1/move",
	"PATCH /api/session/s1",
	"GET /api/session/s1/permission-rules",
	"PUT /api/session/s1/permission-rules",
}

func TestRoutesGuardsAndSessionResolution(t *testing.T) {
	allow := false
	resolved := 0
	d := Deps{
		Guards: module.Guards{Auth: func(next http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if !allow {
					http.Error(w, "auth guard", http.StatusUnauthorized)
					return
				}
				next(w, r)
			}
		}},
		Privileged: func(*http.Request) bool { return true },
		WithSessionPath: func(w http.ResponseWriter, _ *http.Request, _ func(http.ResponseWriter, *http.Request, string, string)) {
			resolved++
			http.Error(w, "core path rejection", http.StatusBadRequest)
		},
		WithSessionAdapter: func(w http.ResponseWriter, _ *http.Request, _ func(http.ResponseWriter, *http.Request, string, string, platforms.Platform)) {
			resolved++
			http.Error(w, "core adapter rejection", http.StatusBadRequest)
		},
	}
	mux := http.NewServeMux()
	m := NewModule(d)
	m.Routes(routeTestRouter{mux: mux})
	if m.Name() != "sessionlifecycle" {
		t.Fatal(m.Name())
	}
	for _, route := range directRoutes {
		t.Run(route, func(t *testing.T) {
			method, path, _ := strings.Cut(route, " ")
			allow = false
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			if rr.Code != http.StatusUnauthorized || resolved != 0 {
				t.Fatalf("unguarded route: status=%d resolved=%d", rr.Code, resolved)
			}
			// Core owns session validation. Its rejection must stop dispatch.
			allow = true
			rr = httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(method, path, strings.NewReader(`{}`)))
			if rr.Code != http.StatusBadRequest || resolved != 1 {
				t.Fatalf("core resolver bypassed: status=%d resolved=%d", rr.Code, resolved)
			}
			resolved = 0
			rr = httptest.NewRecorder()
			mux.ServeHTTP(rr, httptest.NewRequest(http.MethodDelete, path, nil))
			if rr.Code != http.StatusMethodNotAllowed || resolved != 0 {
				t.Fatalf("wrong method reached handler: status=%d resolved=%d", rr.Code, resolved)
			}
		})
	}
	for _, path := range []string{"/api/sessions/prepare", "/api/sessions/start"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, path, nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s bypassed Local: %d", path, rr.Code)
		}
	}
	for _, path := range []string{"message", "permissions/p1", "questions/q1"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/session/s1/"+path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("module owns core route %s: %d", path, rr.Code)
		}
	}
}

func TestRestartAndReloadRequirePrivilegeAfterAuth(t *testing.T) {
	mux := http.NewServeMux()
	NewModule(Deps{
		Guards:     module.Guards{Auth: func(h http.HandlerFunc) http.HandlerFunc { return h }},
		Privileged: func(*http.Request) bool { return false },
	}).Routes(routeTestRouter{mux: mux})
	for _, action := range []string{"restart-opencode", "reload-opencode"} {
		rr := httptest.NewRecorder()
		mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/api/session/s1/"+action, nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: status=%d", action, rr.Code)
		}
	}
}

type actionTestSessions struct {
	Sessions
	call func(context.Context, string, any) error
}

func (s actionTestSessions) ExecuteCommand(c context.Context, p string, r platforms.ExecuteCommandRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) RunShell(c context.Context, p string, r platforms.RunShellRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Rename(c context.Context, p string, r platforms.RenameSessionRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Abort(c context.Context, p string, r platforms.AbortRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Revert(c context.Context, p string, r platforms.RevertSessionRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Unrevert(c context.Context, p string, r platforms.UnrevertSessionRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Compact(c context.Context, p string, r platforms.CompactRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) Fork(c context.Context, p string, r platforms.ForkSessionRequest) (*platforms.CreateSessionResponse, error) {
	if err := s.call(c, p, r); err != nil {
		return nil, err
	}
	return &platforms.CreateSessionResponse{ID: "child"}, nil
}
func (s actionTestSessions) Move(c context.Context, p string, r platforms.MoveSessionRequest) error {
	return s.call(c, p, r)
}
func (s actionTestSessions) SetPermissionRules(c context.Context, p string, r platforms.SetPermissionRulesRequest) error {
	return s.call(c, p, r)
}

func TestActionDependencies(t *testing.T) {
	rules := []platforms.PermissionRule{{Permission: "bash", Pattern: "git *", Action: "allow"}}
	for _, tc := range []struct {
		name   string
		method string
		body   string
		want   any
	}{
		{"command", "POST", `{"command":"test","arguments":"all","model":"p/m","agent":"build","reasoning":"high"}`, platforms.ExecuteCommandRequest{SessionID: "s1", Command: "test", Arguments: "all", Model: "p/m", Agent: "build", Reasoning: "high"}},
		{"shell", "POST", `{"command":"pwd","agent":"build"}`, platforms.RunShellRequest{SessionID: "s1", Command: "pwd", Agent: "build"}},
		{"", "PATCH", `{"title":"renamed"}`, platforms.RenameSessionRequest{SessionID: "s1", Title: "renamed"}},
		{"abort", "POST", `{}`, platforms.AbortRequest{SessionID: "s1"}},
		{"revert", "POST", `{"messageID":"msg1"}`, platforms.RevertSessionRequest{SessionID: "s1", MessageID: "msg1"}},
		{"unrevert", "POST", `{}`, platforms.UnrevertSessionRequest{SessionID: "s1"}},
		{"compact", "POST", `{"providerID":"p","modelID":"m"}`, platforms.CompactRequest{SessionID: "s1", ProviderID: "p", ModelID: "m"}},
		{"fork", "POST", `{"messageID":"msg1"}`, platforms.ForkSessionRequest{SessionID: "s1", MessageID: "msg1"}},
		{"move", "POST", `{"directory":"/repo"}`, platforms.MoveSessionRequest{SessionID: "s1", Directory: "/repo"}},
		{"permission-rules", "PUT", `{"rules":[{"permission":"bash","pattern":"git *","action":"allow"}]}`, platforms.SetPermissionRulesRequest{SessionID: "s1", Rules: rules}},
	} {
		t.Run(tc.method+"/"+tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			current := actionTestSessions{call: func(c context.Context, p string, req any) error {
				calls++
				// Abort detaches from the request (TestAbortOutlivesClientDisconnect).
				if (c != ctx && tc.name != "abort") || p != "r-owner:opencode" || !reflect.DeepEqual(req, tc.want) {
					t.Fatalf("dependency received ctx=%v platform=%q request=%+v; want %+v", c, p, req, tc.want)
				}
				return nil
			}}
			m := NewModule(Deps{
				Sessions: func() Sessions { return current },
				Guards:   module.Guards{Auth: func(h http.HandlerFunc) http.HandlerFunc { return h }},
				WithSessionPath: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string)) {
					fn(w, r, r.PathValue("id"), tc.name)
				},
			})
			mux := http.NewServeMux()
			m.Routes(routeTestRouter{mux: mux})
			path := "/api/session/s1"
			if tc.name != "" {
				path += "/" + tc.name
			}
			path += "?platform=%20r-owner:opencode%20"
			run := func(body string) *httptest.ResponseRecorder {
				rr := httptest.NewRecorder()
				mux.ServeHTTP(rr, httptest.NewRequest(tc.method, path, strings.NewReader(body)).WithContext(ctx))
				return rr
			}
			rr := run(tc.body)
			wantStatus := http.StatusNoContent
			if tc.name == "fork" {
				wantStatus = http.StatusOK
			}
			if rr.Code != wantStatus || calls != 1 {
				t.Fatalf("status=%d calls=%d body=%s", rr.Code, calls, rr.Body)
			}
			if tc.name == "fork" && strings.TrimSpace(rr.Body.String()) != `{"id":"child"}` {
				t.Fatal(rr.Body.String())
			}
			// Replacing the dependency must affect the next call.
			current = actionTestSessions{call: func(context.Context, string, any) error { return platforms.ErrUnsupported }}
			if rr := run(tc.body); rr.Code != http.StatusNotImplemented {
				t.Fatalf("stale dependency: %d %s", rr.Code, rr.Body)
			}
			if tc.name != "abort" && tc.name != "unrevert" {
				if rr := run(`{`); rr.Code != http.StatusBadRequest {
					t.Fatalf("invalid JSON accepted: %d", rr.Code)
				}
			}
		})
	}
}

// A flaky client connection must not cancel the stop the user asked for.
func TestAbortOutlivesClientDisconnect(t *testing.T) {
	var got error
	m := NewModule(Deps{
		Sessions: func() Sessions {
			return actionTestSessions{call: func(c context.Context, _ string, _ any) error { got = c.Err(); return nil }}
		},
		Guards: module.Guards{Auth: func(h http.HandlerFunc) http.HandlerFunc { return h }},
		WithSessionPath: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string)) {
			fn(w, r, r.PathValue("id"), "")
		},
	})
	mux := http.NewServeMux()
	m.Routes(routeTestRouter{mux: mux})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/session/s1/abort", nil).WithContext(ctx))
	if got != nil {
		t.Fatalf("abort ran on a cancelled context: %v", got)
	}
}

type rulesTestPlatform struct {
	platforms.Platform
	rules []platforms.PermissionRule
	err   error
}

func (p rulesTestPlatform) PermissionRules(_ context.Context, id string) ([]platforms.PermissionRule, error) {
	if id != "s1" {
		panic("wrong session ID")
	}
	return p.rules, p.err
}

func TestPermissionRulesRead(t *testing.T) {
	for _, tc := range []struct {
		name     string
		platform rulesTestPlatform
		status   int
	}{
		{"inherited", rulesTestPlatform{}, http.StatusOK},
		{"explicit", rulesTestPlatform{rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}}, http.StatusOK},
		{"unsupported", rulesTestPlatform{err: platforms.ErrUnsupported}, http.StatusNotImplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModule(Deps{WithSessionAdapter: func(w http.ResponseWriter, r *http.Request, fn func(http.ResponseWriter, *http.Request, string, string, platforms.Platform)) {
				fn(w, r, "s1", "permission-rules", tc.platform)
			}})
			rr := httptest.NewRecorder()
			m.HandleSessionPermissionRulesGet(rr, httptest.NewRequest(http.MethodGet, "/api/session/s1/permission-rules", nil))
			if rr.Code != tc.status {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			if tc.status == http.StatusOK {
				var got struct {
					Rules []platforms.PermissionRule `json:"rules"`
				}
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got.Rules == nil || len(got.Rules) != len(tc.platform.rules) {
					t.Fatalf("rules=%+v", got.Rules)
				}
				if len(got.Rules) > 0 && !reflect.DeepEqual(got.Rules, tc.platform.rules) {
					t.Fatal(got.Rules)
				}
			}
		})
	}
}
