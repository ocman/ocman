package factorymod

import (
	mcpserver "github.com/mark3labs/mcp-go/server"
)

// testTools registers both Factory tools over svc without the agent-facing
// refusals Module.Tools adds, so each tool action is tested directly.
func testTools(svc toolService) []mcpserver.ServerTool {
	return append(factoryServerTools(&factoryTools{svc: svc}), factoryControlServerTools(svc)...)
}
