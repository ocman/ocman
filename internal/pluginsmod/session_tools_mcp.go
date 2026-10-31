package pluginsmod

import (
	"context"
	"encoding/json"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tools exposes a single generic dispatcher. Neither plugin selection nor a
// caller-provided session ID can widen the authority in a session's binding.
func (m *Module) Tools() []server.ServerTool {
	return []server.ServerTool{{Tool: mcplib.NewTool("plugin_session_tool",
		mcplib.WithDescription("Call a plugin-owned tool using the private binding supplied to your managed session. Tool names and schemas are provided in that session's prompt. Never share the binding."),
		mcplib.WithString("binding", mcplib.Required()), mcplib.WithString("tool", mcplib.Required()), mcplib.WithObject("arguments", mcplib.Required())), Handler: m.handleSessionTool}}
}

func (m *Module) handleSessionTool(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
	args, ok := req.GetArguments()["arguments"].(map[string]any)
	if !ok {
		return mcplib.NewToolResultError("arguments must be an object"), nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return mcplib.NewToolResultError("invalid arguments"), nil
	}
	result, err := m.callSessionTool(ctx, req.GetString("binding", ""), req.GetString("tool", ""), raw)
	if err != nil {
		return mcplib.NewToolResultError("plugin tool unavailable, refused, or its session limit was reached"), nil
	}
	return mcplib.NewToolResultText(string(result)), nil
}
