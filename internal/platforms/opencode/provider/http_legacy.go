package opencode

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
)

// Package helpers retain the legacy process-default transport. Named account
// callers use the corresponding Adapter methods instead.
func InvalidateCatalogsForPort(port string)               { legacyHTTP().InvalidateCatalogsForPort(port) }
func fetchOpenCodeMCP(port string) map[string]rawMCPEntry { return legacyHTTP().fetchOpenCodeMCP(port) }
func fetchOpenCodeLSP(port string) []rawLSPEntry          { return legacyHTTP().fetchOpenCodeLSP(port) }
func getInto(port, path string, v interface{}) bool       { return legacyHTTP().getInto(port, path, v) }
func fetchOpenCodeSmallModel(port string) (providerID, modelID string, ok bool) {
	return legacyHTTP().fetchOpenCodeSmallModel(port)
}
func AgentNames(ctx context.Context, port, directory string) []string {
	return legacyHTTP().AgentNames(ctx, port, directory)
}
func getJSON(ctx context.Context, port, path string) ([]byte, error) {
	return legacyHTTP().getJSON(ctx, port, path)
}
func getJSONCached(ctx context.Context, port, path string) ([]byte, error) {
	return legacyHTTP().getJSONCached(ctx, port, path)
}
func ProjectCatalog(ctx context.Context, endpoint, directory string) (agents, models []string, err error) {
	return legacyHTTP().ProjectCatalog(ctx, endpoint, directory)
}
func agentCatalogAt(ctx context.Context, port, sessionID, directory string) []platforms.AgentCatalogEntry {
	return legacyHTTP().agentCatalogAt(ctx, port, sessionID, directory)
}
func slashCommandsAt(ctx context.Context, port, sessionID, directory string) []platforms.SlashCommandEntry {
	return legacyHTTP().slashCommandsAt(ctx, port, sessionID, directory)
}
func runShellOnPort(ctx context.Context, port string, req platforms.RunShellRequest) error {
	return legacyHTTP().runShellOnPort(ctx, port, req)
}
func permissionRulesOnPort(port, sessionID string) ([]platforms.PermissionRule, error) {
	return legacyHTTP().permissionRulesOnPort(port, sessionID)
}
func WorktreeName(ctx context.Context, port, directory, prompt string) (string, error) {
	return legacyHTTP().WorktreeName(ctx, port, directory, prompt)
}
func probeSessionPort(ctx context.Context, port, id string) (bool, error) {
	return legacyHTTP().probeSessionPort(ctx, port, id)
}
