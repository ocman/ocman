package server

import (
	"context"
	"errors"
	"time"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/opencode/provider"
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
		DefaultEnabled: s.AutoApproveDefault,
		OpenCode:       s.openCodeEnabled,
		Watchers:       s.openCodeWatchers,
		ModelEntries:   s.reviewerModelEntries,
	})
}

func (s *Server) autoapproveEngineDeps() autoapprove.Deps {
	return s.autoapproveEngineDepsFor(opencode.PlatformID, s.legacyOpenCodePlatform)
}

func (s *Server) autoapproveEngineDepsFor(platformID platforms.ID, resolve func() platforms.Platform) autoapprove.Deps {
	deps := autoapprove.Deps{
		JudgeUserMessages: s.permissionJudgeUserMessages,
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
			session, err := s.sessionMetadata(ctx, string(platformID), childID)
			if err != nil || session == nil || session.ParentID == "" {
				return "", false
			}
			return session.ParentID, true
		},
		OpencodePlatform: resolve,
		ReviewerPlatform: func() platforms.Platform { return s.reviewerPlatform(resolve) },
		BroadcastPermissionResolved: func(platform, session, request, reason string) {
			s.onAgentPromptResolved(platform, session, "permission", request)
			s.broadcastPermissionResolved(platform, session, request, reason)
		},
		PermissionAsked: s.notifyPermissionInbox,
		PromptNeedsUser: s.onPromptNeedsUser,
		BroadcastQuestionResolved: func(platform, session, request, reason string) {
			s.onAgentPromptResolved(platform, session, "question", request)
			s.broadcastResolved("ocman.question.resolved", "requestId", platform, session, request, reason)
		},
		BroadcastSessionIdle: func(platform, session string) {
			s.onAgentWaitIdle(platform, session)
			s.onSessionIdle(platform, session)
		},
		BroadcastQueueChanged: func(platformID, sessionID string) {
			ctx, cancel := s.lifetimeCall()
			defer cancel()
			s.broadcastQueueUpdated(ctx, platformID, sessionID)
		},
		// The retry, change and title hooks fire from the OpenCode watcher only.
		SessionRetry: func(session string, status autoapprove.SessionStatus) {
			s.onSessionRetry(string(platformID), session, status)
		},
		BroadcastSessionChanged: func(session string) {
			s.broadcastSessionChanged(string(platformID), session)
		},
		BroadcastSessionStatus: func(session string, status db.SessionStatus) {
			s.sessionEventSink().OnSessionStatus(string(platformID), session, status, 0)
		},
		BroadcastSessionTitle: func(session, title string) {
			s.broadcastSessionTitle(string(platformID), session, title)
		},
		BroadcastGlobalEvent: s.broadcastGlobalEvent,
		BroadcastGitCommand: func(sessionID, action string) {
			// broadcastGitCommand bounds its own reads.
			s.broadcastGitCommand(s.lifetime(), string(platformID), sessionID, action)
		},
		DefaultEnabled:     s.autoApproveDefault,
		DefaultEnabledFunc: s.AutoApproveDefault,
	}
	deps.RefreshSession = func(ctx context.Context, sessionID string) error {
		if cache, ok := resolve().(platforms.SessionCacheMaintainer); ok {
			return cache.RefreshSession(ctx, sessionID)
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
		deps.OpenCodeDisabled = s.reviewerPlatform(resolve) == nil
		for _, p := range s.registry.Platforms() {
			if j, ok := p.(interface{ PermissionJudge() autoapprove.Judge }); ok {
				deps.FallbackJudge = j.PermissionJudge()
				break
			}
		}
	}
	return deps
}

func (s *Server) permissionJudgeUserMessages(ctx context.Context, platform platforms.ID, id string) (autoapprove.JudgeUserContext, error) {
	if !s.hasStoredHistory() || s.historyIngest == nil {
		return autoapprove.JudgeUserContext{}, platforms.ErrHistoryUnavailable
	}
	owner, kind := platforms.SplitPlatformID(string(platform))
	if owner != "" {
		return autoapprove.JudgeUserContext{}, platforms.ErrUnsupported
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	// Every authorization read, including the final recheck, needs its own
	// session-specific receipt. Equal retained snapshots may both be stale.
	if err := s.historyIngest.RefreshStored(ctx, string(platform), id); err != nil {
		return autoapprove.JudgeUserContext{}, err
	}
	userContext, err := s.historyAnalytics.Store.ReviewUserMessages(ctx, s.historyOwner, kind, id, autoapprove.MaxJudgeUserMessageBytes)
	return autoapprove.JudgeUserContext{Messages: userContext.Messages, Limited: userContext.Limited, Digest: userContext.Digest}, err
}

// openCodeEnabled reports whether this machine runs the OpenCode platform.
// Nothing OpenCode-only (judge discovery, its config, skills) runs without
// it. A server without a registry (bare test construction) keeps the
// OpenCode behaviour.
func (s *Server) openCodeEnabled() bool {
	if s.registry == nil {
		return true
	}
	for _, entry := range s.registry.Entries() {
		if isLocalAdapter(entry.Platform) && entry.Platform.ID() == opencode.PlatformID {
			return true
		}
	}
	return false
}

// autoApproveSessionDir reads OpenCode sessions from its database and any
// other platform's sessions through the adapter that owns them.
func (s *Server) autoApproveSessionDir(sessionID string) (string, error) {
	ctx, cancel := s.lifetimeCall()
	defer cancel()
	if row, err := s.sessionMetadata(ctx, "", sessionID); err == nil && row != nil && row.Directory != "" {
		return row.Directory, nil
	}
	return "", errors.New("session not found")
}
