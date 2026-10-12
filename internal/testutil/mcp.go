package testutil

import (
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
)

// CallTool calls one MCP tool on an mcptest server and fails the test on a
// transport error. Tool errors come back in the result.
func CallTool(t *testing.T, srv *mcptest.Server, name string, args map[string]any) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args
	result, err := srv.Client().CallTool(t.Context(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return result
}

// ResultText is the text of a tool result's first content block.
func ResultText(result *mcplib.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if content, ok := result.Content[0].(mcplib.TextContent); ok {
		return content.Text
	}
	return ""
}
