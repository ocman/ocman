package prworkflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestPRToolLooksUpSessionInCheckout(t *testing.T) {
	x := newFixture(t)
	x.m.d.FindSessions = func(ctx context.Context, directory, platform, owner string) ([]Target, error) {
		if directory != "/repo" || platform != "r-box:opencode" || owner != "box" {
			t.Fatalf("lookup scope: %q %q %q", directory, platform, owner)
		}
		return []Target{{Platform: "r-box:opencode", SessionID: "s1", Directory: "/repo", RemoteID: "box"}}, nil
	}
	args := map[string]any{"action": "link", "directory": "/repo", "platform": "r-box:opencode", "remote_id": "box", "host": "github.com", "repo": "team/repo", "number": float64(42)}
	result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(args))
	if err != nil || result.IsError {
		t.Fatalf("lookup link: %+v %v", result, err)
	}
	if l := x.only(t); l.SessionID != "s1" || l.Platform != "r-box:opencode" {
		t.Fatalf("wrong link: %+v", l)
	}
	if _, exists := args["session_id"]; exists {
		t.Fatal("lookup mutated caller arguments")
	}
}

func TestPRToolLookupValidationAndScopes(t *testing.T) {
	for _, tc := range []struct {
		name string
		args map[string]any
		rows []Target
		err  error
		want string
	}{
		{"relative", map[string]any{"directory": "repo"}, nil, nil, "directory must be absolute"},
		{"bad identity", map[string]any{"directory": "/repo", "session_id": 42}, nil, nil, "session_id must be a string"},
		{"bad platform", map[string]any{"directory": "/repo", "platform": 42}, nil, nil, "platform must be a string"},
		{"bad owner", map[string]any{"directory": "/repo", "remote_id": 42}, nil, nil, "remote_id must be a string"},
		{"read failed", map[string]any{"directory": "/repo"}, nil, errors.New("offline"), "lookup failed"},
		{"no matches", map[string]any{"directory": "/repo"}, nil, nil, "no session matches"},
		{"wrong directory", map[string]any{"directory": "/repo"}, []Target{{SessionID: "s1", Platform: "p", Directory: "/other"}}, nil, "no session matches"},
		{"explicit id", map[string]any{"directory": "/repo", "session_id": "s2"}, []Target{{SessionID: "s1", Platform: "p", Directory: "/repo"}}, nil, "no session matches"},
		{"explicit platform", map[string]any{"directory": "/repo", "platform": "p2"}, []Target{{SessionID: "s1", Platform: "p", Directory: "/repo"}}, nil, "no session matches"},
		{"explicit owner", map[string]any{"directory": "/repo", "remote_id": "box"}, []Target{{SessionID: "s1", Platform: "p", Directory: "/repo", RemoteID: "local"}}, nil, "no session matches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(Deps{FindSessions: func(context.Context, string, string, string) ([]Target, error) { return tc.rows, tc.err }})
			tc.args["action"] = "linked"
			_, err := m.lookupToolSession(t.Context(), toolRequest(tc.args))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	if _, err := New(Deps{}).lookupToolSession(t.Context(), toolRequest(map[string]any{"action": "linked", "directory": "/repo"})); err == nil {
		t.Fatal("missing lookup dependency accepted")
	}
}

func TestPRToolLookupOnlyFillsMissingIdentity(t *testing.T) {
	called := false
	m := New(Deps{FindSessions: func(context.Context, string, string, string) ([]Target, error) {
		called = true
		return []Target{{SessionID: "s1", Platform: "cnx_work", Directory: "/repo", RemoteID: "local"}}, nil
	}})
	for _, args := range []map[string]any{
		{"action": "linked", "session_id": "s1", "platform": "cnx_work", "directory": "/repo"},
		{"action": "help", "directory": "/repo"},
		{"action": "list", "directory": "/repo"},
		{"action": "linked"},
	} {
		if _, err := m.lookupToolSession(t.Context(), toolRequest(args)); err != nil || called {
			t.Fatalf("unnecessary lookup: %v, called=%v", err, called)
		}
	}
	for _, args := range []map[string]any{
		{"action": "linked", "directory": "/repo"},
		{"action": "linked", "directory": "/repo", "session_id": "s1"},
		{"action": "linked", "directory": "/repo", "platform": "cnx_work"},
	} {
		req, err := m.lookupToolSession(t.Context(), toolRequest(args))
		if err != nil || req.GetString("session_id", "") != "s1" || req.GetString("platform", "") != "cnx_work" {
			t.Fatalf("lookup identity: %+v %v", req, err)
		}
	}
}

func TestPRToolLookupRefusesAmbiguity(t *testing.T) {
	x := newFixture(t)
	x.m.d.FindSessions = func(context.Context, string, string, string) ([]Target, error) {
		return []Target{{Platform: "opencode", SessionID: "s1", Directory: "/repo"}, {Platform: "opencode", SessionID: "s2", Directory: "/repo"}}, nil
	}
	result, err := x.m.Tools()[0].Handler(t.Context(), toolRequest(map[string]any{"action": "link", "directory": "/repo", "host": "github.com", "repo": "team/repo", "number": float64(42)}))
	if err != nil || !result.IsError {
		t.Fatalf("ambiguous lookup: %+v %v", result, err)
	}
	text := result.Content[0].(mcplib.TextContent).Text
	for _, want := range []string{"ambiguous", "s1", "s2", "opencode"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q: %s", want, text)
		}
	}
}
