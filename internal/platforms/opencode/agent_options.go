package opencode

import "context"

// AgentNames reads only the agent catalog, scoped to the project directory.
// agentCatalogAt logs fetch/decode failures and returns no options on failure.
func AgentNames(ctx context.Context, port, directory string) []string {
	var names []string
	for _, agent := range agentCatalogAt(ctx, port, "", directory) {
		if agent.Name != "" && !agent.Hidden && agent.Mode != "subagent" {
			names = append(names, agent.Name)
		}
	}
	return names
}
