package mcp

import (
	"context"
	"strings"

	"github.com/NoUseFreak/ocman/internal/state"
	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type inboxStore interface {
	CreateSessionInboxItem(context.Context, string, string, string, *state.InboxSession) (state.InboxItem, error)
	RecallInboxItem(context.Context, string) error
}

type inboxTools struct{ store inboxStore }

func inboxServerTools(tools *inboxTools) []server.ServerTool {
	if tools == nil || tools.store == nil {
		return nil
	}
	return []server.ServerTool{{Tool: mcplib.NewTool("inbox",
		mcplib.WithDescription("Send and recall owner-local Inbox items. Use action help for schemas and examples."),
		mcplib.WithString("action", mcplib.Required()), mcplib.WithString("title"), mcplib.WithString("body"), mcplib.WithString("category", mcplib.Enum("general", "factory", "routine")), mcplib.WithString("item_id"),
		mcplib.WithString("session_id", mcplib.Description("Originating coding-agent session ID. Supply with platform when sending from a session.")), mcplib.WithString("platform")), Handler: tools.handle}}
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
			"send":    map[string]any{"required": []string{"title", "body"}, "optional": map[string]string{"category": "general (default), factory, or routine; permission is reserved for live requests", "session_id": "originating session ID; provide together with platform", "platform": "owner-local platform of the originating session"}, "action": "Sends an unread item to this ocman instance's Inbox. Include session_id and platform when sending from a coding-agent session.", "example": `{"action":"send","title":"Review complete","body":"The pull request is ready.","category":"general","session_id":"ses_1","platform":"opencode"}`, "output_schema": map[string]string{"id": "opaque item ID"}},
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
		category := req.GetString("category", state.InboxGeneral)
		if category != state.InboxGeneral && category != state.InboxFactory && category != state.InboxRoutine {
			return mcplib.NewToolResultError("category must be general, factory, or routine"), nil
		}
		platform, sessionID := strings.TrimSpace(req.GetString("platform", "")), strings.TrimSpace(req.GetString("session_id", ""))
		if (platform == "") != (sessionID == "") {
			return mcplib.NewToolResultError("platform and session_id must be provided together"), nil
		}
		if strings.HasPrefix(platform, "r-") {
			return mcplib.NewToolResultError("platform must be owner-local"), nil
		}
		var session *state.InboxSession
		if sessionID != "" {
			session = &state.InboxSession{Platform: platform, SessionID: sessionID}
		}
		item, err := t.store.CreateSessionInboxItem(ctx, title, body, category, session)
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
