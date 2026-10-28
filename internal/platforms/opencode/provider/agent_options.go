package opencode

import (
	"context"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/ocman/ocman/internal/platforms"
)

// AgentNames reads only the agent catalog, scoped to the project directory.
// agentCatalogAt logs fetch/decode failures and returns no options on failure.
func AgentNames(ctx context.Context, port, directory string) []string {
	// Shared fetches outlive a canceled caller but are independently bounded.
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

var defaultAgentCatalog = AgentNames
var defaultAgentPorts func(context.Context) map[string]string
var defaultAgentPort func(context.Context, string) string
var defaultAgentTargetTimeout = 4 * time.Second

func (a *Adapter) AgentOptions(ctx context.Context, directories []string) []string {
	return knownAgentOptions(ctx, a.rt, directories)
}

func knownAgentOptions(ctx context.Context, disc *Discovery, directories []string) []string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	portsFn, portFn := defaultAgentPorts, defaultAgentPort
	if portsFn == nil {
		portsFn = disc.DiscoverOpenCodePortsContext
	}
	if portFn == nil {
		portFn = disc.DiscoverOpenCodePortContext
	}
	known := map[string]bool{"build": true, "plan": true}
	targets := maps.Clone(portsFn(ctx))
	if targets == nil {
		targets = map[string]string{}
	}
	for _, directory := range directories {
		if ctx.Err() != nil {
			break
		}
		if port := portFn(ctx, directory); port != "" {
			targets[directory] = port
		}
	}
	jobs := make(chan string)
	var mu sync.Mutex
	var workers sync.WaitGroup
	for range min(8, len(targets)) {
		workers.Go(func() {
			for directory := range jobs {
				if ctx.Err() != nil {
					return
				}
				targetCtx, cancelTarget := context.WithTimeout(ctx, defaultAgentTargetTimeout)
				agents := defaultAgentCatalog(targetCtx, targets[directory], directory)
				cancelTarget()
				mu.Lock()
				for _, agent := range agents {
					known[agent] = true
				}
				mu.Unlock()
			}
		})
	}
	catalogDirectories := make([]string, 0, len(targets))
	for directory := range targets {
		catalogDirectories = append(catalogDirectories, directory)
	}
	sort.Strings(catalogDirectories)
	for _, directory := range catalogDirectories {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- directory:
		case <-ctx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	agents := make([]string, 0, len(known))
	for agent := range known {
		agents = append(agents, agent)
	}
	sort.Strings(agents)
	return agents
}
