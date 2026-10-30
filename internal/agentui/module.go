package agentui

import (
	"context"
	"encoding/json"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	internalmcp "github.com/ocman/ocman/internal/mcp"
	"github.com/ocman/ocman/internal/module"
)

type Module struct{}

func (*Module) Name() string         { return "agentui" }
func (*Module) Routes(module.Router) {}

func (*Module) Tools() []server.ServerTool {
	return []server.ServerTool{{Tool: mcplib.NewTool("render_ui",
		mcplib.WithDescription("Render a user-friendly result inside this conversation using A2UI v0.9.1 and Ocman's approved catalog: text, layouts, sortable tables, bar charts, and simple forms. Use when structured or interactive results communicate better than prose. Call action help first for the exact catalog, schemas, limits and example. action render takes title, summary and messages. Results appear under this tool call; do not copy JSON into your reply. Buttons require user confirmation before sending an event back to this session; they never execute commands directly. Keep a short text summary for other clients."),
		mcplib.WithString("action", mcplib.Required(), mcplib.Enum("help", "render")),
		mcplib.WithString("title"), mcplib.WithString("summary"),
		mcplib.WithArray("messages", mcplib.Items(map[string]any{"type": "object"}))), Handler: handle}}
}

func handle(_ context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, _ := req.RequireString("action")
	if action == "help" {
		return internalmcp.ToolResultJSON(help()), nil
	}
	if action != "render" {
		return mcplib.NewToolResultError("action must be help or render"), nil
	}
	args := req.GetArguments()
	raw, err := json.Marshal(map[string]any{"title": args["title"], "summary": args["summary"], "messages": args["messages"]})
	if err != nil {
		return mcplib.NewToolResultError("result must be JSON"), nil
	}
	result, err := Validate(raw)
	if err != nil {
		out := internalmcp.ToolResultJSON(map[string]any{"error": map[string]any{
			"code": "VALIDATION_FAILED", "surfaceId": "", "path": "/messages", "message": err.Error(),
		}})
		out.IsError = true
		return out, nil
	}
	return internalmcp.ToolResultJSON(map[string]any{"ocmanA2ui": result, "hint": "The result is displayed below this tool call in Ocman. Do not repeat the JSON. Give the user a short text summary."}), nil
}

func help() map[string]any {
	docs := map[string]any{}
	for _, name := range []string{"common_types.json", "catalog.json", "result.schema.json"} {
		var doc any
		_ = json.Unmarshal(schemaBytes(name), &doc)
		docs[name] = doc
	}
	return map[string]any{
		"version": "v0.9.1", "a2uiClientCapabilities": map[string]any{"supportedCatalogIds": []string{CatalogID}},
		"schemas": docs,
		"rules": []string{
			"Call action render with title, summary, messages. Each result is a self-contained snapshot saved in the tool output; separate calls do not patch earlier results.",
			"Use exactly one createSurface followed by updateComponents and optional updateDataModel messages for the same surfaceId. Every message has version v0.9.1. Include a root component.",
			"Only this custom catalog is supported, not the full A2UI basic catalog. No HTML, JavaScript, URLs, functions, templates, theme overrides, deleteSurface or cross-call updates.",
			"Bindings use one top-level field, e.g. /name. updateDataModel replaces the root object; nested paths and non-scalar data model values are unsupported.",
			"Button child must be Text. action.event.context may reference current form bindings. The user reviews and confirms the resolved action before it is sent as a normal message to this session. Existing session permissions still apply. Read-only views disable actions.",
			"Limits: 64 KiB per result, 32 messages, 100 components, depth 16, 500 rendered nodes. Table supports 200 rows and 12 columns; BarChart supports 50 non-negative values.",
		},
		"example": map[string]any{
			"action": "render", "title": "Investigation", "summary": "Two jobs failed. Choose a follow-up.",
			"messages": []any{
				map[string]any{"version": "v0.9.1", "createSurface": map[string]any{"surfaceId": "investigation", "catalogId": CatalogID}},
				map[string]any{"version": "v0.9.1", "updateComponents": map[string]any{"surfaceId": "investigation", "components": []any{
					map[string]any{"id": "root", "component": "Column", "children": []string{"summary", "jobs", "followup", "button"}},
					map[string]any{"id": "summary", "component": "Text", "text": "Two jobs failed", "variant": "h3"},
					map[string]any{"id": "jobs", "component": "Table", "columns": []string{"Job", "Status"}, "rows": [][]string{{"Backend", "Failed"}, {"Frontend", "Failed"}}},
					map[string]any{"id": "followup", "component": "TextField", "label": "Investigate next", "value": map[string]any{"path": "/job"}},
					map[string]any{"id": "button", "component": "Button", "child": "buttonLabel", "action": map[string]any{"event": map[string]any{"name": "investigate", "context": map[string]any{"job": map[string]any{"path": "/job"}}}}},
					map[string]any{"id": "buttonLabel", "component": "Text", "text": "Continue investigation"},
				}}},
				map[string]any{"version": "v0.9.1", "updateDataModel": map[string]any{"surfaceId": "investigation", "value": map[string]any{"job": "Backend"}}},
			},
		},
	}
}
