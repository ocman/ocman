package server

import (
	"context"
	"sync"

	"github.com/NoUseFreak/ocman/internal/platforms/opencode/apiv2"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode/provider"
)

// Session consumers start only after identity cutover. Kept together so prompt
// watchers, archive jobs and feature workers cannot accidentally bypass
// the preparation fence even though the HTTP application is already serving.
func (s *Server) startSessionServices(ctx context.Context, workers *sync.WaitGroup) error {
	if err := s.startModules(ctx, workers); err != nil {
		return err
	}
	s.watchProviderPrompts(ctx, workers)
	workers.Go(func() { s.runAutoArchiveLoop(ctx) })
	workers.Go(func() { s.runProjectsIndexLoop(ctx) })
	if _, enabled := s.registry.Get(opencode.PlatformID); enabled {
		if health, ok := s.router().Local().(interface{ RunOpenCodeHealthChecks(context.Context) }); ok {
			workers.Go(func() { health.RunOpenCodeHealthChecks(ctx) })
		}
		workers.Go(func() { ocv2.WatchInstalledVersion(ctx) })
		workers.Go(ensureOpenCodeMCPLayout)
		if sup, ok := s.router().Local().(interface{ RunMachineSupervisor(context.Context) }); ok {
			workers.Go(func() { sup.RunMachineSupervisor(ctx) })
		}
	}
	return nil
}
