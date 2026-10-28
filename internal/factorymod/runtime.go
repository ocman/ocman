package factorymod

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

// ensurePlatformMCP verifies OpenCode loaded ocman's MCP server. Provider
// launches pass the MCP address themselves, so there is nothing to check.
func (l PlanningLauncher) ensurePlatformMCP(ctx context.Context, platform string, runtime *hostsvc.EnsureRuntimeResult) error {
	if _, base := platforms.SplitPlatformID(platform); base == string(platforms.OpenCodeID) {
		return l.ensureMCP(ctx, runtime)
	}
	return nil
}

// factoryMCPRules returns the rule deciding one ocman MCP tool. Claude asks
// for every MCP call and names it mcp__ocman__<tool>, so providers also get
// that rule; OpenCode keeps its single existing rule.
func factoryMCPRules(platform string, rule platforms.PermissionRule) []platforms.PermissionRule {
	if _, base := platforms.SplitPlatformID(platform); base == string(platforms.OpenCodeID) {
		return []platforms.PermissionRule{rule}
	}
	return []platforms.PermissionRule{rule, {Permission: "mcp__ocman__" + rule.Pattern, Pattern: "*", Action: rule.Action}}
}
