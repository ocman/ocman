package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
)

func newPermissionRulesTestServer(t *testing.T, fake *fakePlatform) *Server {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	fake.sessions = []db.Session{mkSession(string(fake.ID()), "sess-1", "t", 1000)}
	if fake.sessionDetailFn == nil {
		fake.sessionDetailFn = func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &fake.sessions[0]}, nil
		}
	}
	reg.Register(fake)
	return srv
}

func TestSessionPermissionRules_Get(t *testing.T) {
	for _, tc := range []struct {
		name     string
		rulesFn  func(string) ([]platforms.PermissionRule, error)
		want     int
		wantBody string
	}{
		{
			name: "rules",
			rulesFn: func(sessionID string) ([]platforms.PermissionRule, error) {
				if sessionID != "sess-1" {
					t.Errorf("sessionID = %q, want sess-1", sessionID)
				}
				return []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}}, nil
			},
			want:     http.StatusOK,
			wantBody: `"rules":[{"permission":"edit","pattern":"*","action":"deny"}]`,
		},
		{
			name:     "nil rules render as empty array",
			rulesFn:  func(string) ([]platforms.PermissionRule, error) { return nil, nil },
			want:     http.StatusOK,
			wantBody: `"rules":[]`,
		},
		{name: "unsupported", want: http.StatusNotImplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newPermissionRulesTestServer(t, &fakePlatform{id: "fake", permissionRulesFn: tc.rulesFn})
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, httptest.NewRequest(http.MethodGet, "/api/session/sess-1/permission-rules", nil))
			if rr.Code != tc.want || !strings.Contains(rr.Body.String(), tc.wantBody) {
				t.Fatalf("status = %d, want %d; body=%s, want %s", rr.Code, tc.want, rr.Body, tc.wantBody)
			}
		})
	}
}

func TestSessionPermissionRules_Put(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []platforms.PermissionRule
	}{
		{
			// Missing pattern defaults to "*".
			name: "rules",
			body: `{"rules":[{"permission":"edit","pattern":"*","action":"allow"},{"permission":"bash","action":"ask"}]}`,
			want: []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}, {Permission: "bash", Pattern: "*", Action: "ask"}},
		},
		{name: "empty restores defaults", body: `{"rules":[]}`, want: []platforms.PermissionRule{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *platforms.SetPermissionRulesRequest
			srv := newPermissionRulesTestServer(t, &fakePlatform{
				id: "fake",
				setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
					got = &req
					return nil
				},
			})
			rr := httptest.NewRecorder()
			serveRoutes(t, srv, rr, httptest.NewRequest(http.MethodPut, "/api/session/sess-1/permission-rules", strings.NewReader(tc.body)))
			if rr.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
			}
			if got == nil || got.SessionID != "sess-1" || len(got.Rules) != len(tc.want) {
				t.Fatalf("adapter got %+v, want %+v for sess-1", got, tc.want)
			}
			for i := range tc.want {
				if got.Rules[i] != tc.want[i] {
					t.Errorf("rules[%d] = %+v, want %+v", i, got.Rules[i], tc.want[i])
				}
			}
		})
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
		serveRoutes(t, srv, rr, req)
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
			serveRoutes(t, srv, rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body)
			}
			if called {
				t.Error("adapter was called despite invalid input")
			}
		})
	}
}
