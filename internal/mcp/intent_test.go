package mcp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/mcp"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
)

func TestToolsAcceptOptionalIntent(t *testing.T) {
	contributed := mcplib.NewTool("contributed", mcplib.WithString("action", mcplib.Required()))
	deps := mcp.Deps{SessionService: &fakeSessionService{}, Tools: []server.ServerTool{{Tool: contributed, Handler: func(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		return mcplib.NewToolResultText(req.GetString("action", "")), nil
	}}}}
	tools := mcp.ServerTools(deps)
	for _, tool := range tools {
		t.Run(tool.Tool.Name, func(t *testing.T) {
			property, ok := tool.Tool.InputSchema.Properties["intent"].(map[string]any)
			if !ok || property["type"] != "string" || property["description"] == "" {
				t.Fatalf("intent schema = %#v", property)
			}
			if !strings.Contains(property["description"].(string), "Include on every call") {
				t.Errorf("intent description does not encourage use: %s", property["description"])
			}
			for _, required := range tool.Tool.InputSchema.Required {
				if required == "intent" {
					t.Fatal("intent must be optional")
				}
			}
		})
	}
	if _, ok := contributed.InputSchema.Properties["intent"]; ok {
		t.Fatal("registration mutated the contributed tool")
	}
	srv, err := mcptest.NewServer(t, tools...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv.Close)
	for _, intent := range []string{"", "Check the current operation"} {
		args := map[string]any{"action": "list"}
		if intent != "" {
			args["intent"] = intent
		}
		result := callTool(t, srv, "contributed", args)
		if result.IsError || resultText(result) != "list" {
			t.Fatalf("intent changed operation: %#v", result)
		}
	}
}
