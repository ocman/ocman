package server

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectsmod"
)

// restoreSession clears project inheritance only for this session's owner.
// Ordinary reads and activity-based resurfacing must not restore projects.
func (s *Server) restoreSession(ctx context.Context, platform, sessionID string) error {
	projects, err := s.stateDB.ArchivedProjects(ctx)
	if err != nil {
		return err
	}
	if len(projects) > 0 {
		route, ok := s.publicSessionPlatform(platform, sessionID)
		if !ok {
			return platforms.ErrNotFound
		}
		adapter, ok := s.registry.Get(platforms.ID(route))
		if !ok {
			return platforms.ErrNotFound
		}
		detail, err := adapter.Session(ctx, sessionID, 0, 0)
		if err != nil {
			return err
		}
		if detail == nil || detail.Session == nil {
			return platforms.ErrNotFound
		}
		owner, _ := platforms.SplitPlatformID(route)
		key := projectsmod.ArchiveKey(owner, detail.Session.Directory)
		if _, archived := projects[key]; archived {
			if err := s.stateDB.UnarchiveProject(ctx, key.RemoteID, key.Root); err != nil {
				return err
			}
		}
	}
	return s.stateDB.UnarchiveSession(ctx, platform, sessionID)
}
