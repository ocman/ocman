package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func newPermissionRulesTestServer(t *testing.T, fake *fakePlatform) *Server {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	fake.sessions = []db.Session{mkSession(string(fake.ID()), "sess-1", "t", 1000)}
	reg.Register(fake)
	return srv
}

func TestSessionPermissionRules_Get(t *testing.T) {
	fake := &fakePlatform{
		id: "fake",
		permissionRulesFn: func(sessionID string) ([]platforms.PermissionRule, error) {
			if sessionID != "sess-1" {
				t.Errorf("sessionID = %q, want sess-1", sessionID)
			}
			return []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}}, nil
		},
	}
	srv := newPermissionRulesTestServer(t, fake)

	req := httptest.NewRequest("GET", "/api/session/sess-1/permission-rules", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body)
	}
	var resp struct {
		Rules []platforms.PermissionRule `json:"rules"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(resp.Rules) != 1 || resp.Rules[0].Action != "deny" {
		t.Errorf("rules = %+v, want one deny rule", resp.Rules)
	}
}

func TestSessionPermissionRules_GetNilRulesReturnsEmptyArray(t *testing.T) {
	fake := &fakePlatform{
		id: "fake",
		permissionRulesFn: func(string) ([]platforms.PermissionRule, error) {
			return nil, nil
		},
	}
	srv := newPermissionRulesTestServer(t, fake)

	req := httptest.NewRequest("GET", "/api/session/sess-1/permission-rules", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"rules":[]`) {
		t.Errorf("body = %s, want \"rules\":[]", rr.Body)
	}
}

func TestSessionPermissionRules_GetUnsupported(t *testing.T) {
	srv := newPermissionRulesTestServer(t, &fakePlatform{id: "fake"})

	req := httptest.NewRequest("GET", "/api/session/sess-1/permission-rules", nil)
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", rr.Code)
	}
}

func TestSessionPermissionRules_Put(t *testing.T) {
	var got *platforms.SetPermissionRulesRequest
	fake := &fakePlatform{
		id: "fake",
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			got = &req
			return nil
		},
	}
	srv := newPermissionRulesTestServer(t, fake)

	body := `{"rules":[{"permission":"edit","pattern":"*","action":"allow"},{"permission":"bash","action":"ask"}]}`
	req := httptest.NewRequest("PUT", "/api/session/sess-1/permission-rules", strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if got == nil || got.SessionID != "sess-1" || len(got.Rules) != 2 {
		t.Fatalf("adapter got %+v, want 2 rules for sess-1", got)
	}
	// Missing pattern defaults to "*".
	if got.Rules[1].Pattern != "*" {
		t.Errorf("rules[1].Pattern = %q, want default *", got.Rules[1].Pattern)
	}
}

func TestSessionPermissionRules_PutEmptyRestoresDefaults(t *testing.T) {
	var got *platforms.SetPermissionRulesRequest
	fake := &fakePlatform{
		id: "fake",
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			got = &req
			return nil
		},
	}
	srv := newPermissionRulesTestServer(t, fake)

	req := httptest.NewRequest("PUT", "/api/session/sess-1/permission-rules", strings.NewReader(`{"rules":[]}`))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if got == nil || len(got.Rules) != 0 {
		t.Fatalf("adapter got %+v, want empty ruleset", got)
	}
}

func TestSessionPermissionRules_PutValidation(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"bad action", `{"rules":[{"permission":"edit","pattern":"*","action":"yolo"}]}`},
		{"missing permission", `{"rules":[{"pattern":"*","action":"allow"}]}`},
		{"not json", `nope`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			fake := &fakePlatform{
				id: "fake",
				setPermissionRulesFn: func(platforms.SetPermissionRulesRequest) error {
					called = true
					return nil
				},
			}
			srv := newPermissionRulesTestServer(t, fake)

			req := httptest.NewRequest("PUT", "/api/session/sess-1/permission-rules", strings.NewReader(c.body))
			rr := httptest.NewRecorder()
			srv.dispatchSessionSubpath(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body)
			}
			if called {
				t.Error("adapter was called despite invalid input")
			}
		})
	}
}
