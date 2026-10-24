package server

import (
	"context"
	"errors"

	"github.com/NoUseFreak/ocman/internal/autoapprove"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/opencode"
)

// autoapprove_engine.go adapts server internals (state DB, OpenCode DB,
// registry, broadcast hub) to the auto-approve module and its pipeline.

// aaSvc is the auto-approve pipeline the server's permission paths drive.
func (s *Server) aaSvc() *autoapprove.Service { return s.autoapproveModule().Service() }

// autoapproveModule is built lazily on first use so bare &Server{} test
// constructions work and the dependency fields are wired by then. Tests may
// assign s.autoapproveMod before that first use.
func (s *Server) autoapproveModule() *autoapprove.Module {
	return s.autoapproveMod.get(func() *autoapprove.Module {
		return s.newAutoapproveModule(s.autoapproveEngineDeps())
	})
}

func (s *Server) newAutoapproveModule(engine autoapprove.Deps) *autoapprove.Module {
	return autoapprove.NewModule(autoapprove.ModuleDeps{
		Engine:         engine,
		Store:          s.stateDB,
		Session:        s.resolvePlatformForSession,
		DefaultEnabled: func() bool { return s.autoApproveDefault },
		OpenCode:       s.openCodeEnabled,
	})
}

func (s *Server) autoapproveEngineDeps() autoapprove.Deps {
	deps := autoapprove.Deps{
		RecentUserMessages: func(ctx context.Context, platform platforms.ID, id string) ([]string, error) {
			if !s.hasStoredHistory() {
				return nil, platforms.ErrHistoryUnavailable
			}
			owner, kind := platforms.SplitPlatformID(string(platform))
			if owner != "" {
				return nil, platforms.ErrUnsupported
			}
			return s.historyAnalytics.Store.RecentUserMessages(ctx, s.historyOwner, kind, id, 6)
		},
		OpenCodeAuth:     s.openCodeAuth,
		SessionDir:       s.autoApproveSessionDir,
		PermissionParent: permissionPolicyParent,
		ParentSessionID: func(ctx context.Context, childID string) (string, bool) {
			session, err := s.sessionMetadata(ctx, string(opencode.PlatformID), childID)
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
		BroadcastPermissionResolved: func(platform, session, request, reason string) {
			s.onAgentPromptResolved(platform, session, "permission", request)
			s.broadcastPermissionResolved(platform, session, request, reason)
		},
		PermissionAsked: s.notifyPermissionInbox,
		PromptNeedsUser: s.onPromptNeedsUser,
		BroadcastQuestionResolved: func(platform, session, request, reason string) {
			s.onAgentPromptResolved(platform, session, "question", request)
			s.broadcastQuestionResolved(platform, session, request, reason)
		},
		BroadcastSessionIdle: func(platform, session string) {
			s.onAgentWaitIdle(platform, session)
			s.onSessionIdle(platform, session)
		},
		BroadcastQueueChanged: func(platformID, sessionID string) {
			s.broadcastQueueUpdated(context.Background(), platformID, sessionID)
		},
		// The retry, change and title hooks fire from the OpenCode watcher only.
		SessionRetry: func(session string, status autoapprove.SessionStatus) {
			s.onSessionRetry(string(opencode.PlatformID), session, status)
		},
		BroadcastSessionChanged: func(session string) {
			s.broadcastSessionChanged(string(opencode.PlatformID), session)
		},
		BroadcastSessionStatus: s.onLocalSessionStatus,
		BroadcastSessionTitle: func(session, title string) {
			s.broadcastSessionTitle(string(opencode.PlatformID), session, title)
		},
		BroadcastGlobalEvent: s.broadcastGlobalEvent,
		BroadcastGitCommand: func(sessionID, action string) {
			s.broadcastGitCommand(context.Background(), string(opencode.PlatformID), sessionID, action)
		},
		DefaultEnabled: s.autoApproveDefault,
	}
	deps.RefreshSession = func(ctx context.Context, sessionID string) error {
		if s.registry != nil {
			if adapter, ok := s.registry.Get(opencode.PlatformID); ok {
				if cache, ok := adapter.(platforms.SessionCacheMaintainer); ok {
					return cache.RefreshSession(ctx, sessionID)
				}
			}
		}
		return platforms.ErrUnsupported
	}
	// Only wire the store when a state DB exists — a nil *state.DB
	// inside a non-nil interface would defeat the service's nil checks.
	if s.stateDB != nil {
		deps.Store = s.stateDB
		deps.CommitStore = s.stateDB
		deps.RecordTypeSafeUsage = s.analyticsModule().RecordTypeSafeUsage
	}
	// Without a reachable OpenCode instance, a platform's own judge decides (AD-8).
	if s.registry != nil {
		deps.OpenCodeDisabled = !s.openCodeEnabled()
		for _, p := range s.registry.Platforms() {
			if j, ok := p.(interface{ PermissionJudge() autoapprove.Judge }); ok {
				deps.FallbackJudge = j.PermissionJudge()
				break
			}
		}
	}
	return deps
}

// openCodeEnabled reports whether this machine runs the OpenCode platform.
// Nothing OpenCode-only (judge discovery, its config, skills) runs without
// it. A server without a registry (bare test construction) keeps the
// OpenCode behaviour.
func (s *Server) openCodeEnabled() bool {
	if s.registry == nil {
		return true
	}
	_, ok := s.registry.Get(opencode.PlatformID)
	return ok
}

// autoApproveSessionDir reads OpenCode sessions from its database and any
// other platform's sessions through the adapter that owns them.
func (s *Server) autoApproveSessionDir(sessionID string) (string, error) {
	ctx := context.Background()
	if row, err := s.sessionMetadata(ctx, "", sessionID); err == nil && row != nil && row.Directory != "" {
		return row.Directory, nil
	}
	return "", errors.New("session not found")
}

func (s *Server) onLocalSessionStatus(sessionID string, status db.SessionStatus) {
	s.sessionEventSink().OnSessionStatus(string(opencode.PlatformID), sessionID, status, 0)
}
