package server

import (
	"context"
	"strings"
	"testing"

	internalmcp "github.com/NoUseFreak/ocman/internal/mcp"
	mcplib "github.com/mark3labs/mcp-go/mcp"
)

func TestFeatureMCPBoundaryUsesOwnedIDsAndPreservesOpaqueArguments(t *testing.T) {
	s, _, ownedID := identityHTTPFixture(t)
	args := map[string]any{"action": "create", "platform": "test", "session_id": ownedID, "prompt": ownedID,
		"files": []any{map[string]any{"content": ownedID}},
	}
	req := mcplib.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = "artifacts", args
	handler := s.sessionIdentityToolMiddleware(func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if req.GetString("session_id", "") != "native" || req.GetString("prompt", "") != ownedID {
			t.Fatalf("wrong argument projection: %+v", req.GetArguments())
		}
		return internalmcp.ToolResultJSON(map[string]any{"id": "artifact", "platform": "test", "sessionId": "native", "items": []map[string]string{{"content": ownedID}}}), nil
	})
	result, err := handler(t.Context(), req)
	if err != nil || result.IsError {
		t.Fatalf("tool failed: %+v %v", result, err)
	}
	text := result.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, `"sessionId":"`+ownedID+`"`) || !strings.Contains(text, `"id":"artifact"`) {
		t.Fatalf("tool emitted native reference: %s", text)
	}
	if args["session_id"] != ownedID {
		t.Fatal("mutated caller-owned arguments")
	}
}

func TestFeatureMCPUnknownOwnedReferenceNeverReachesNativeHandler(t *testing.T) {
	s, _, _ := identityHTTPFixture(t)
	req := mcplib.CallToolRequest{}
	req.Params.Name, req.Params.Arguments = "routines", map[string]any{"action": "create", "session_id": "ocm_missing"}
	result, err := s.sessionIdentityToolMiddleware(func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		t.Fatal("unknown Ocman identity fell back to a native handler")
		return nil, nil
	})(t.Context(), req)
	if err != nil || !result.IsError {
		t.Fatalf("unknown identity not rejected: %+v %v", result, err)
	}
}
