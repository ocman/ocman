package server

import (
	"context"
	"fmt"

	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/worktreecleanup"
)

func (s *Server) worktreeCleanupModule() *worktreecleanup.Module {
	return worktreecleanup.New(worktreecleanup.Deps{
		Store:     s.stateDB,
		Platforms: func() []platforms.Platform { return s.registry.Platforms() },
		Router:    s.router,
		Idle: func(ctx context.Context) error {
			count, err := s.taskManager().ActiveCount(ctx)
			if err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("owner has pending background tasks")
			}
			return nil
		},
		Ready: func(id string) bool {
			remote, _ := platforms.SplitPlatformID(id)
			return remote != "" || s.historyIngest == nil || s.historyIngest.InitialImportReady(id)
		},
	})
}

func (s *Server) beginWorktreeSessionActivity(ctx context.Context, p platforms.Platform, id string) (context.Context, func(), error) {
	return s.worktreeCleanupModule().BeginSession(ctx, p, id)
}

func (s *Server) beginWorktreeDirectoryActivity(ctx context.Context, dir string) (context.Context, func(), error) {
	return s.worktreeCleanupModule().BeginDirectory(ctx, dir)
}

func (s *Server) beginWorktreePlatformDirectoryActivity(ctx context.Context, p platforms.Platform, dir string) (context.Context, func(), error) {
	owner, _ := platforms.SplitPlatformID(string(p.ID()))
	return s.worktreeCleanupModule().BeginOwnerDirectory(ctx, owner, dir)
}

func (s *Server) admitWorktreeCleanup(ctx context.Context, req hostsvc.CleanupWorktreeRequest) (context.Context, func(), error) {
	return s.worktreeCleanupModule().AdmitCleanup(ctx, req)
}
