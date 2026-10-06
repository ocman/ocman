package opencode

import (
	"context"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// AgentNames reads only the agent catalog, scoped to the project directory.
// agentCatalogAt logs fetch/decode failures and returns no options on failure.
func AgentNames(ctx context.Context, port, directory string) []string {
	// ponytail: one worker per read; the shared cache wait itself cannot be
	// canceled, but its upstream request is bounded by the HTTP timeout.
	ready := make(chan []platforms.AgentCatalogEntry, 1)
	go func() { ready <- agentCatalogAt(ctx, port, "", directory) }()
	var agents []platforms.AgentCatalogEntry
	select {
	case agents = <-ready:
	case <-ctx.Done():
		return nil
	}
	var names []string
	for _, agent := range agents {
		if agent.Name != "" && !agent.Hidden && agent.Mode != "subagent" {
			names = append(names, agent.Name)
		}
	}
	return names
}
