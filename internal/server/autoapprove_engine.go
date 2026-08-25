package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// autoapprove_engine.go wires the internal/autoapprove domain package
// into the server: it adapts server internals (state DB, OpenCode DB,
// registry, broadcast hub) to the package's Deps seam, mirroring
// autoapprove engine wiring.

// aaSvc returns the auto-approve service, building it lazily on first
// use so bare &Server{} test constructions work and so the dependency
// fields (stateDB, db, registry, broadcast hub) are wired by the time
// the service is created.
func (s *Server) aaSvc() *autoapprove.Service {
	s.aaOnce.Do(func() {
		deps := autoapprove.Deps{
			OpenCodeAuth: s.openCodeAuth,
			SessionDir: func(sessionID string) (string, error) {
				if s.db == nil {
					return "", errors.New("no OpenCode DB")
				}
				sess, err := s.db.GetSession(context.Background(), sessionID)
				if err != nil {
					return "", err
				}
				if sess == nil {
					return "", errors.New("session not found")
				}
				return sess.Directory, nil
			},
			ParentSessionID: func(ctx context.Context, childID string) (string, bool) {
				if s.db == nil {
					return "", false
				}
				session, err := s.db.GetSession(ctx, childID)
				if err != nil || session == nil || session.ParentID == "" {
					return "", false
				}
				return session.ParentID, true
			},
			OpencodePlatform: func() platforms.Platform {
				if s.registry == nil {
					return nil
				}
				if p, ok := s.registry.Get(opencode.PlatformID); ok {
					return p
				}
				return nil
			},
			BroadcastPermissionResolved: s.broadcastPermissionResolved,
			BroadcastQuestionResolved:   s.broadcastQuestionResolved,
			BroadcastSessionIdle:        s.onSessionIdle,
			BroadcastSessionChanged:     s.broadcastSessionChanged,
			BroadcastSessionStatus:      s.onLocalSessionStatus,
			BroadcastGlobalEvent:        s.broadcastGlobalEvent,
			DefaultEnabled:              s.autoApproveDefault,
		}
		if s.db != nil {
			deps.RefreshSession = func(ctx context.Context, sessionID string) error {
				return opencode.RefreshSession(ctx, s.db, sessionID)
			}
		}
		// Only wire the store when a state DB exists — a nil *state.DB
		// inside a non-nil interface would defeat the service's nil
		// checks.
		if s.stateDB != nil {
			deps.Store = s.stateDB
		}
		s.aaSvcCached = autoapprove.NewService(deps)
	})
	return s.aaSvcCached
}

func (s *Server) onLocalSessionStatus(sessionID string, status db.SessionStatus) {
	s.broadcastSessionStatus(sessionID, status)
}
