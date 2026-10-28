package server

import (
	"context"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/ocmaint"
	"github.com/ocman/ocman/internal/ocruntime"
	"github.com/ocman/ocman/internal/platforms"
)

// WithOpenCodeDBPath enables OpenCode database maintenance for the
// database at path. Must be called before Start.
func (s *Server) WithOpenCodeDBPath(path string) *Server {
	if path == "" {
		return s
	}
	local := func() hostsvc.Host { return s.router().Local() }
	s.maintMod = ocmaint.NewModule(ocmaint.New(ocmaint.Deps{
		DBPath:   path,
		DumpPath: path + ".ocman-diffs",
		Instances: func(ctx context.Context) ([]string, error) {
			managed, err := local().ManagedOpencodes(ctx)
			roots := make([]string, len(managed))
			for i, m := range managed {
				roots[i] = m.RepoRoot
			}
			return roots, err
		},
		Stop: func(ctx context.Context, root string) error {
			return local().StopProjectOpencode(ctx, hostsvc.EnsureProjectOpencodeRequest{ProjectDir: root})
		},
		Start: func(ctx context.Context, root string) error {
			_, err := local().EnsureRuntime(ctx, string(platforms.OpenCodeID), root)
			return err
		},
		Holders:   ocmaint.LsofHolders,
		FreeBytes: ocmaint.FreeBytes,
	}))
	return s
}

// maintenanceModule answers that maintenance is unavailable until
// WithOpenCodeDBPath configured it.
func (s *Server) maintenanceModule() *ocmaint.Module {
	if s.maintMod == nil {
		return ocmaint.NewModule(nil)
	}
	return s.maintMod
}

// launchBlocked reports why opencode may not launch right now.
func (s *Server) launchBlocked() error { return s.maintenanceModule().LaunchBlocked() }

// gatedRuntime refuses launches while maintenance runs; probe and stop
// pass through so the job itself can stop instances.
type gatedRuntime struct {
	ocruntime.Runtime
	blocked func() error
}

func (g gatedRuntime) Launch(ctx context.Context, spec ocruntime.LaunchSpec) (*ocruntime.Instance, error) {
	if err := g.blocked(); err != nil {
		return nil, err
	}
	return g.Runtime.Launch(ctx, spec)
}

func (s *Server) gatedRuntime() ocruntime.Runtime {
	rt := s.runtime
	if rt == nil {
		rt = ocruntime.NewNativeRuntime()
	}
	return gatedRuntime{Runtime: rt, blocked: s.launchBlocked}
}

func (s *Server) gateLaunchTmux(launch func(context.Context, string) (string, error)) func(context.Context, string) (string, error) {
	return func(ctx context.Context, dir string) (string, error) {
		if err := s.launchBlocked(); err != nil {
			return "", err
		}
		return launch(ctx, dir)
	}
}
