package mcp

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type inboxStore interface {
	CreateInboxItem(context.Context, string, string) (state.InboxItem, error)
	RecallInboxItem(context.Context, string) error
}

type inboxTools struct{ store inboxStore }

func inboxServerTools(tools *inboxTools) []server.ServerTool {
	if tools == nil || tools.store == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("inbox",
		mcplib.WithDescription("Send and recall owner-local Inbox items. Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("title"), mcplib.WithString("body"), mcplib.WithString("item_id")), Handler: tools.handle}}
}

func addInboxTools(s *server.MCPServer, tools *inboxTools) {
	for _, tool := range inboxServerTools(tools) {
		s.AddTool(tool.Tool, tool.Handler)
	}
}

func (t *inboxTools) handle(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	action, err := req.RequireString("action")
	if err != nil {
		return mcplib.NewToolResultError("action is required"), nil
	}
	switch action {
	case "help":
		return toolResultJSON(map[string]any{
			"actions": []string{"help", "send", "recall"},
			"help":    map[string]any{"action": "Describes every available Inbox action.", "example": `{"action":"help"}`, "output_schema": "Inbox action documentation"},
			"send":    map[string]any{"required": []string{"title", "body"}, "action": "Sends an unread item to this ocman instance's Inbox.", "example": `{"action":"send","title":"Review complete","body":"The pull request is ready."}`, "output_schema": map[string]string{"id": "opaque item ID"}},
			"recall":  map[string]any{"required": []string{"item_id"}, "action": "Recalls an item. Unknown and already recalled IDs succeed.", "example": `{"action":"recall","item_id":"opaque-id"}`, "output_schema": map[string]string{"status": "recalled"}},
			"rules":   []string{"title and body must be nonblank", "item IDs are opaque and owner-local"},
			"errors":  []string{"action is required", "unknown action", "title is required", "body is required", "item_id is required", "inbox request failed"},
		}), nil
	case "send":
		title, body := strings.TrimSpace(req.GetString("title", "")), strings.TrimSpace(req.GetString("body", ""))
		if title == "" {
			return mcplib.NewToolResultError("title is required"), nil
		}
		if body == "" {
			return mcplib.NewToolResultError("body is required"), nil
		}
		item, err := t.store.CreateInboxItem(ctx, title, body)
		if err != nil {
			return mcplib.NewToolResultError("inbox request failed"), nil
		}
		return toolResultJSON(map[string]string{"id": item.ID}), nil
	case "recall":
		id := strings.TrimSpace(req.GetString("item_id", ""))
		if id == "" {
			return mcplib.NewToolResultError("item_id is required"), nil
		}
		if err := t.store.RecallInboxItem(ctx, id); err != nil {
			return mcplib.NewToolResultError("inbox request failed"), nil
		}
		return toolResultJSON(map[string]string{"status": "recalled"}), nil
	default:
		return mcplib.NewToolResultError("unknown action"), nil
	}
}
