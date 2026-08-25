package mcp_test

import (
	"context"
	"database/sql"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	_ "modernc.org/sqlite"

	"github.com/NoUseFreak/ocman/internal/state"
)

func openTestStateDB(t *testing.T) *state.DB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("opening test state db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sdb, err := state.OpenFromSQL(sqlDB)
	if err != nil {
		t.Fatalf("initializing state schema: %v", err)
	}
	return sdb
}

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
