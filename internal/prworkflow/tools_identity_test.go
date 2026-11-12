package prworkflow

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestPRToolMissingIdentityExplainsRecovery(t *testing.T) {
	for _, action := range []string{"linked", "link", "unlink", "watch", "unwatch", "feedback", "checks_prompt", "conflicts_prompt"} {
		for _, field := range []string{"session_id", "platform"} {
			for _, value := range []any{nil, "", " \n", 42} {
				t.Run(action+"/"+field+"/"+fmt.Sprint(value), func(t *testing.T) {
					args := map[string]any{"action": action, "session_id": "s1", "platform": "opencode", "host": "github.com", "repo": "team/repo", "number": float64(42)}
					if value == nil {
						delete(args, field)
					} else {
						args[field] = value
					}
					called := false
					h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })
					result := handlePRTool(t.Context(), h, toolRequest(args))
					if !result.IsError || called {
						t.Fatalf("missing identity reached handler: %+v, called=%v", result, called)
					}
					text := result.Content[0].(mcplib.TextContent).Text
					for _, guidance := range []string{field, "sessions", "search", "get", "current checkout", "conversation", "retry", "Do not guess", "PR"} {
						if !strings.Contains(text, guidance) {
							t.Errorf("missing recovery guidance %q in %q", guidance, text)
						}
					}
				})
			}
		}
	}
}

func TestPRToolMalformedIdentityStillRejected(t *testing.T) {
	for _, id := range []string{"a/b", "%2e%2e", "..", " s1", "s?platform=other"} {
		result := handlePRTool(t.Context(), nil, toolRequest(map[string]any{"action": "link", "session_id": id, "platform": "opencode"}))
		if !result.IsError || result.Content[0].(mcplib.TextContent).Text != "session_id must be a nonempty path segment" {
			t.Fatalf("malformed identity %q: %+v", id, result)
		}
	}
}

func TestPRToolIdentityGuidanceIsDiscoverable(t *testing.T) {
	x := newFixture(t)
	for name, text := range map[string]string{"description": x.m.Tools()[0].Tool.Description, "help": prToolHelp().Content[0].(mcplib.TextContent).Text} {
		t.Run(name, func(t *testing.T) {
			for _, guidance := range []string{"session_id", "platform", "sessions", "search", "get", "current checkout", "conversation", "Do not guess"} {
				if !strings.Contains(text, guidance) {
					t.Errorf("missing identity guidance %q", guidance)
				}
			}
		})
	}
}
