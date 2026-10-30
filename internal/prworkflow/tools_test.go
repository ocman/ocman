package prworkflow

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func toolRequest(args map[string]any) mcplib.CallToolRequest {
	var req mcplib.CallToolRequest
	req.Params.Arguments = args
	return req
}

func TestPRToolTeachesPostCreationLinking(t *testing.T) {
	x := newFixture(t)
	help := prToolHelp().Content[0].(mcplib.TextContent).Text
	for name, text := range map[string]string{"description": x.m.Tools()[0].Tool.Description, "help": help} {
		t.Run(name, func(t *testing.T) {
			for _, guidance := range []string{"After creating a PR", "action link", "action linked", "current session", "before reporting completion", "PR URL", "Do not enable watching unless requested"} {
				if !strings.Contains(text, guidance) {
					t.Errorf("missing post-creation guidance: %s", guidance)
				}
			}
		})
	}
}

func TestPRToolRoutes(t *testing.T) {
	for _, tc := range []struct{ action, method, path, bodyAction string }{
		{"upstreams", "GET", "/api/project/upstreams", ""},
		{"list", "GET", "/api/project/prs", ""},
		{"checks", "GET", "/api/project/pr-checks", ""},
		{"mergeability", "GET", "/api/project/pr-mergeability", ""},
		{"launch", "POST", "/api/project/handle", "review"},
		{"linked", "GET", "/api/session/s1/pull-requests", ""},
		{"link", "POST", "/api/session/s1/pull-requests", "link"},
		{"unlink", "POST", "/api/session/s1/pull-requests", "unlink"},
		{"watch", "POST", "/api/session/s1/pull-requests", "watch"},
		{"unwatch", "POST", "/api/session/s1/pull-requests", "unwatch"},
		{"feedback", "POST", "/api/session/s1/pull-request-feedback", "feedback"},
		{"checks_prompt", "POST", "/api/session/s1/pull-request-feedback", "checks"},
		{"conflicts_prompt", "POST", "/api/session/s1/pull-request-feedback", "conflicts"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			args := map[string]any{"action": tc.action, "directory": "/src/a b", "remote_id": "box", "remote": "origin", "session_id": "s1", "platform": "r-box:opencode", "host": "github.com", "repo": "a/b", "number": float64(42), "sha": "abc", "state": "all", "mine": "alice", "page": float64(2), "refresh": true, "mode": "worktree", "task": "review", "fetchHead": true}
			method, path, body, err := prToolRequest(tc.action, toolRequest(args))
			if err != nil {
				t.Fatal(err)
			}
			u, err := url.Parse(path)
			if err != nil || method != tc.method || u.Path != tc.path {
				t.Fatalf("%s %s %v", method, path, err)
			}
			q := u.Query()
			if q.Get("dir") != "/src/a b" || q.Get("remoteId") != "box" || q.Get("platform") != "r-box:opencode" || q.Get("refresh") != "1" || q.Get("number") != "42" || q.Get("page") != "2" {
				t.Fatalf("query: %v", q)
			}
			var payload map[string]any
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatal(err)
			}
			if method == "POST" && payload["action"] != tc.bodyAction {
				t.Fatalf("payload: %s", body)
			}
			if tc.action == "launch" && payload["type"] != "pr" {
				t.Fatalf("not PR-only: %s", body)
			}
			if _, ok := payload["fetchHead"]; ok {
				t.Fatal("MCP bypassed fetch confirmation")
			}
		})
	}
}

func TestPRToolValidation(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		args         map[string]any
	}{
		{"unknown", "merge", nil},
		{"project owner", "list", map[string]any{"directory": "/repo"}},
		{"project directory", "upstreams", map[string]any{"remote_id": "local"}},
		{"remote", "list", map[string]any{"directory": "/repo", "remote_id": "local"}},
		{"sha", "checks", map[string]any{"directory": "/repo", "remote_id": "local", "remote": "origin"}},
		{"empty session", "linked", map[string]any{"session_id": ""}},
		{"session slash", "linked", map[string]any{"session_id": "a/b"}},
		{"session escape", "linked", map[string]any{"session_id": "%2e%2e"}},
		{"session query", "linked", map[string]any{"session_id": "s?platform=other"}},
		{"session dots", "linked", map[string]any{"session_id": ".."}},
		{"session whitespace", "linked", map[string]any{"session_id": " s"}},
		{"platform", "linked", map[string]any{"session_id": "s1"}},
		{"string", "linked", map[string]any{"session_id": "s1", "platform": "opencode", "state": 1}},
		{"refresh", "linked", map[string]any{"session_id": "s1", "platform": "opencode", "refresh": "yes"}},
		{"page", "linked", map[string]any{"session_id": "s1", "platform": "opencode", "page": 0.5}},
		{"no number", "link", map[string]any{"session_id": "s1", "platform": "opencode", "remote": "origin"}},
		{"negative number", "link", map[string]any{"session_id": "s1", "platform": "opencode", "remote": "origin", "number": -1.0}},
		{"fraction number", "link", map[string]any{"session_id": "s1", "platform": "opencode", "remote": "origin", "number": 1.5}},
		{"huge number", "link", map[string]any{"session_id": "s1", "platform": "opencode", "remote": "origin", "number": 1e50}},
		{"bad task", "launch", map[string]any{"directory": "/repo", "remote_id": "local", "remote": "origin", "platform": "opencode", "mode": "session", "task": "merge"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, _, err := prToolRequest(tc.action, toolRequest(tc.args)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestPRToolResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		body string
		fail bool
	}{
		{"JSON", 200, `{"prs":[]}`, false},
		{"no content", 204, "", false},
		{"bad JSON", 200, "not JSON", true},
		{"owner disconnected", 503, "Session owner disconnected", true},
		{"rate limit", 429, `{"error":"rate_limited"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON header")
				}
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			})
			result := handlePRTool(t.Context(), h, toolRequest(map[string]any{"action": "upstreams", "directory": "/repo", "remote_id": "local"}))
			if result.IsError != tc.fail {
				t.Fatalf("result: %+v", result)
			}
		})
	}
	for _, args := range []map[string]any{nil, {"action": "bad"}, {"action": "help"}, {"action": "linked", "session_id": "s\n1", "platform": "opencode"}} {
		result := handlePRTool(t.Context(), nil, toolRequest(args))
		if result.IsError != (args["action"] != "help") {
			t.Fatalf("result: %+v", result)
		}
	}
}

func TestPRToolSessionLifecycle(t *testing.T) {
	x := newFixture(t)
	tools := x.m.Tools()
	if len(tools) != 1 || tools[0].Tool.Name != "pull_requests" {
		t.Fatalf("tools: %+v", tools)
	}
	call := func(action string, fail bool) *mcplib.CallToolResult {
		t.Helper()
		result, err := tools[0].Handler(t.Context(), toolRequest(map[string]any{"action": action, "session_id": "s1", "platform": "r-box:opencode", "host": "github.com", "repo": "team/repo", "remote": "origin", "number": float64(42)}))
		if err != nil || result.IsError != fail {
			t.Fatalf("%s: %+v %v", action, result, err)
		}
		return result
	}
	call("link", false)
	call("watch", false)
	if !x.only(t).Watching {
		t.Fatal("watch not persisted")
	}
	call("linked", false)
	for _, action := range []string{"feedback", "checks_prompt", "conflicts_prompt"} {
		result := call(action, false)
		text := result.Content[0].(mcplib.TextContent).Text
		if !strings.Contains(text, "prompt") || len(x.sent) != 0 {
			t.Fatalf("draft action sent a message or lacked prompt: %s", text)
		}
	}
	x.ownerErr = errors.New("disconnected")
	call("feedback", true)
	x.ownerErr = nil
	call("unwatch", false)
	if x.only(t).Watching {
		t.Fatal("watch still active")
	}
	call("unlink", false)
	call("watch", true)
	call("linked", false)
}

func TestPRToolProjectDelegation(t *testing.T) {
	x := newFixture(t)
	args := map[string]any{"action": "list", "directory": "/repo", "remote_id": "box", "remote": "origin"}
	result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(args))
	if err != nil || !result.IsError {
		t.Fatalf("missing project dependency: %+v %v", result, err)
	}
	called := false
	x.m.d.ProjectHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/project/prs" || r.URL.Query().Get("remoteId") != "box" {
			t.Fatalf("wrong route/owner: %s", r.URL)
		}
		_, _ = w.Write([]byte(`{"prs":[]}`))
	})
	result, err = x.m.Tools()[0].Handler(t.Context(), toolRequest(args))
	if err != nil || result.IsError || !called {
		t.Fatalf("delegation: %+v %v called=%v", result, err, called)
	}
}
