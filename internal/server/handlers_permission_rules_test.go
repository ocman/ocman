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

	req := httptest.NewRequest(http.MethodGet, "/api/session/sess-1/permission-rules", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/session/sess-1/permission-rules", nil)
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

	req := httptest.NewRequest(http.MethodGet, "/api/session/sess-1/permission-rules", nil)
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
	req := httptest.NewRequest(http.MethodPut, "/api/session/sess-1/permission-rules", strings.NewReader(body))
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

	req := httptest.NewRequest(http.MethodPut, "/api/session/sess-1/permission-rules", strings.NewReader(`{"rules":[]}`))
	rr := httptest.NewRecorder()
	srv.dispatchSessionSubpath(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if got == nil || len(got.Rules) != 0 {
		t.Fatalf("adapter got %+v, want empty ruleset", got)
	}
}

// TestSessionPermissionRules_PutYoloAnswersPendingPrompts: OpenCode reads the
// rules once per turn, so switching to YOLO must clear what is already asked,
// including a subagent's prompt, rather than wait for the next turn.
func TestSessionPermissionRules_PutYoloAnswersPendingPrompts(t *testing.T) {
	var rules []platforms.PermissionRule
	var replied []platforms.RespondPermissionRequest
	fake := &fakePlatform{
		id:   "fake",
		caps: platforms.Capabilities{PermissionRules: true},
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			rules = req.Rules
			return nil
		},
		permissionRulesFn: func(sessionID string) ([]platforms.PermissionRule, error) {
			if sessionID != "sess-1" {
				return nil, nil
			}
			return rules, nil
		},
		listPermissionsFn: func(string) ([]platforms.LivePrompt, error) {
			return []platforms.LivePrompt{
				{"id": "p1", "sessionID": "sess-1", "permission": "bash", "patterns": []any{"rm -rf build"}},
				{"id": "p2", "sessionID": "sess-1", "permission": "edit", "patterns": []any{"main.go"}},
			}, nil
		},
		respondPermissionFn: func(req platforms.RespondPermissionRequest) error {
			replied = append(replied, req)
			return nil
		},
	}
	srv := newPermissionRulesTestServer(t, fake)

	put := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPut, "/api/session/sess-1/permission-rules", strings.NewReader(body))
		rr := httptest.NewRecorder()
		srv.dispatchSessionSubpath(rr, req)
		if rr.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
		}
	}
	put(`{"rules":[{"permission":"edit","pattern":"*","action":"allow"},{"permission":"bash","pattern":"*","action":"ask"}]}`)
	if len(replied) != 1 || replied[0].PermissionID != "p2" || replied[0].Reply != "once" {
		t.Fatalf("auto-edit replies = %+v, want only p2 once", replied)
	}
	put(`{"rules":[{"permission":"*","pattern":"*","action":"allow"}]}`)
	if len(replied) != 2 || replied[1].PermissionID != "p1" {
		t.Fatalf("yolo replies = %+v, want p1 answered once more", replied)
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

			req := httptest.NewRequest(http.MethodPut, "/api/session/sess-1/permission-rules", strings.NewReader(c.body))
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
