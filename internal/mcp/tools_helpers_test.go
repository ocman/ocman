package mcp_test

import (
	"context"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
)

func callTool(t *testing.T, srv *mcptest.Server, toolName string, args map[string]interface{}) *mcplib.CallToolResult {
	t.Helper()
	req := mcplib.CallToolRequest{}
	req.Params.Name = toolName
	req.Params.Arguments = args
	result, err := srv.Client().CallTool(context.Background(), req)
	if err != nil {
		t.Fatalf("CallTool %s: %v", toolName, err)
	}
	return result
}

func resultText(result *mcplib.CallToolResult) string {
	if len(result.Content) == 0 {
		return ""
	}
	if content, ok := result.Content[0].(mcplib.TextContent); ok {
		return content.Text
	}
	return ""
}
