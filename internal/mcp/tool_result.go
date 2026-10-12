package mcp

import (
	"encoding/json"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"
)

// ToolResultJSON renders v as an indented JSON text result. Modules that
// contribute tools use it so every tool answers in the same shape.
func ToolResultJSON(v any) *mcplib.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcplib.NewToolResultText(fmt.Sprintf("%v", v))
	}
	return mcplib.NewToolResultText(string(b))
}

var toolResultJSON = ToolResultJSON
