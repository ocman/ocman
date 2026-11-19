package prworkflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/ocman/ocman/internal/forge"
)

func toolText(t *testing.T, result *mcplib.CallToolResult) string {
	t.Helper()
	return result.Content[0].(mcplib.TextContent).Text
}

// Agents call checks with the identity that link and watch accept. The head
// commit must be resolved from the PR rather than demanded from the caller.
func TestPRToolChecksAcceptsLinkIdentity(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	f := &linkedChecksForge{fakeForge: x.f}
	x.m.d.ResolveForge = func(forge.Remote) (forge.Forge, bool) { return f, true }
	x.m.d.FindSessions = func(context.Context, string, string, string) ([]Target, error) {
		return []Target{{Platform: "r-box:opencode", SessionID: "s1", Directory: "/repo", RemoteID: "box"}}, nil
	}
	for name, args := range map[string]map[string]any{
		"session":   {"session_id": "s1", "platform": "r-box:opencode"},
		"directory": {"directory": "/repo"},
	} {
		t.Run(name, func(t *testing.T) {
			f.sha = ""
			args["action"], args["host"], args["repo"], args["number"] = "checks", "github.com", "team/repo", float64(42)
			result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(args))
			if err != nil || result.IsError {
				t.Fatalf("checks: %s %v", toolText(t, result), err)
			}
			if f.repo != "team/repo" || f.sha != "abc" {
				t.Fatalf("checks target = %q@%q, want team/repo@abc", f.repo, f.sha)
			}
			var out struct {
				State forge.CIState `json:"state"`
				SHA   string        `json:"sha"`
			}
			if err := json.Unmarshal([]byte(toolText(t, result)), &out); err != nil || out.State != forge.CIStatePending || out.SHA != "abc" {
				t.Fatalf("result: %s %v", toolText(t, result), err)
			}
		})
	}
	// An explicit sha still selects that commit.
	result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(map[string]any{"action": "checks", "session_id": "s1", "platform": "r-box:opencode", "host": "github.com", "repo": "team/repo", "number": float64(42), "sha": "def0"}))
	if err != nil || result.IsError || f.sha != "def0" {
		t.Fatalf("explicit sha: %s %v %q", toolText(t, result), err, f.sha)
	}
}

func TestLinkedChecksWithoutHeadSHA(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.f.pr.HeadSHA = ""
	x.request(t, "pull-request-checks?host=github.com&repo=team/repo&number=42", "", 502)
	x.f.pr.HeadSHA = "abc"
	x.f.err = context.DeadlineExceeded
	x.request(t, "pull-request-checks?host=github.com&repo=team/repo&number=42", "", 502)
}

func TestPRToolReportsAllMissingFields(t *testing.T) {
	for _, tc := range []struct {
		name, action string
		args         map[string]any
		want         []string
	}{
		{"checks without identity", "checks", map[string]any{"number": float64(42)}, []string{"session_id", "platform", "directory", "host", "repo"}},
		{"checks with host only", "checks", map[string]any{"host": "github.com", "number": float64(42)}, []string{"session_id", "platform", "repo"}},
		{"project checks", "checks", map[string]any{"remote": "origin"}, []string{"directory", "remote_id", "sha"}},
		{"launch", "launch", map[string]any{}, []string{"directory", "remote_id", "remote", "platform", "mode", "task", "number"}},
		{"watch", "watch", map[string]any{"session_id": "s1"}, []string{"platform", "host", "repo", "number"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, _, err := prToolRequest(tc.action, toolRequest(tc.args))
			if err == nil {
				t.Fatal("expected validation error")
			}
			for _, field := range tc.want {
				if !strings.Contains(err.Error(), field) {
					t.Errorf("error %q does not name %s", err, field)
				}
			}
		})
	}
}

func TestPRToolLinkedIsCompactAndFilterable(t *testing.T) {
	x := newFixture(t)
	x.link(t)
	x.f.pr = forge.PR{Number: 43, Title: "Second", Status: "open", URL: "https://github.com/team/repo/pull/43", Body: strings.Repeat("long body ", 200), HeadSHA: "def"}
	x.request(t, "pull-requests", `{"action":"link","remote":"origin","number":43}`, 200)
	x.watch(t)
	call := func(args map[string]any) []map[string]any {
		t.Helper()
		args["action"], args["session_id"], args["platform"] = "linked", "s1", "r-box:opencode"
		result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(args))
		if err != nil || result.IsError {
			t.Fatalf("linked: %s %v", toolText(t, result), err)
		}
		var out struct {
			PullRequests []map[string]any `json:"pullRequests"`
		}
		if err := json.Unmarshal([]byte(toolText(t, result)), &out); err != nil {
			t.Fatal(err)
		}
		return out.PullRequests
	}
	all := call(map[string]any{})
	if len(all) != 2 {
		t.Fatalf("links = %v", all)
	}
	for _, link := range all {
		if _, ok := link["pr"]; ok {
			t.Fatalf("compact output contains full PR metadata: %v", link)
		}
		remote, _ := link["remote"].(map[string]any)
		if link["number"] == nil || link["state"] != "open" || link["url"] == nil || remote["host"] != "github.com" || remote["repo"] != "team/repo" {
			t.Fatalf("compact link = %v", link)
		}
		if _, ok := link["watching"]; !ok {
			t.Fatalf("compact link lacks watch state: %v", link)
		}
	}
	one := call(map[string]any{"number": float64(43)})
	if len(one) != 1 || one[0]["number"] != float64(43) || one[0]["title"] != "Second" {
		t.Fatalf("filtered = %v", one)
	}
	if none := call(map[string]any{"number": float64(43), "repo": "team/other"}); len(none) != 0 {
		t.Fatalf("repo filter = %v", none)
	}
	full := call(map[string]any{"number": float64(43), "detail": true})
	if len(full) != 1 {
		t.Fatalf("detail = %v", full)
	}
	if pr, _ := full[0]["pr"].(map[string]any); pr["body"] == nil {
		t.Fatalf("detail lacks full metadata: %v", full[0])
	}
}

func TestPRToolProjectChecksRouteIsUnchanged(t *testing.T) {
	method, path, _, err := prToolRequest("checks", toolRequest(map[string]any{"directory": "/repo", "remote_id": "local", "remote": "origin", "sha": "abc", "number": float64(42)}))
	if err != nil || method != "GET" || !strings.HasPrefix(path, "/api/project/pr-checks?") || !strings.Contains(path, "sha=abc") {
		t.Fatalf("%s %s %v", method, path, err)
	}
}
