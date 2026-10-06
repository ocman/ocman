package opencode

import (
	"context"
	"time"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// AgentNames reads only the agent catalog, scoped to the project directory.
// agentCatalogAt logs fetch/decode failures and returns no options on failure.
func AgentNames(ctx context.Context, port, directory string) []string {
	// ponytail: one worker per read; shared fetches outlive a canceled caller
	// but are bounded independently so another catalog reader keeps its result.
	ready := make(chan []platforms.AgentCatalogEntry, 1)
	go func() {
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		ready <- agentCatalogAt(fetchCtx, port, "", directory)
	}()
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
