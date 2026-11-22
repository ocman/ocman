package server

import (
	"context"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/projectsmod"
	log "github.com/sirupsen/logrus"
)

// Resurface after direct delivery or queue admission, not after the turn ends.
// Use the server lifetime so a disconnected client cannot lose the archive update
// after delivery. A persistence failure must not make the client replay the send.
func (s *Server) resurfaceAfterUserMessage(platform, sessionID string) {
	if s.stateDB == nil {
		return
	}
	ctx, cancel := s.lifetimeCall()
	defer cancel()
	if platform == "" {
		p, ok := s.registry.PlatformForSession(ctx, sessionID)
		if !ok {
			return
		}
		key, _ := s.registry.KeyFor(p)
		platform = string(key)
	}
	platform = s.sessionStatePlatform(platform)
	unlock := s.deferredArchives.lock()
	defer unlock()
	changed, err := s.stateDB.UnarchiveAfterUserMessage(ctx, platform, sessionID)
	if err != nil {
		log.WithError(err).WithFields(log.Fields{"platform": platform, "sessionID": sessionID}).Warn("unarchiving session after user message")
		return
	}
	if changed {
		s.deferredArchives.cancelLocked(platform, sessionID)
		s.broadcastSessionPatch(platform, sessionID, map[string]any{"archived": false})
	}
}

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
