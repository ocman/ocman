package server

import (
	"context"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// inheritedRules is the parent's ruleset to seed a worktree child with.
// note is a soft-fail message; empty on success or when skipped.
type inheritedRules struct {
	platform string
	rules    []platforms.PermissionRule
	count    int
	note     string
}

// buildInheritedPermissions reads the parent's always-allow approvals and
// live ruleset when the worktree.inherit_permissions setting is on and a
// parent was named. It needs no child, so it runs alongside creation.
// platform names the adapter owning both sessions; empty means "opencode".
func (s *Server) buildInheritedPermissions(r *http.Request, platform, parentSessionID string) inheritedRules {
	if s.stateDB == nil || parentSessionID == "" {
		return inheritedRules{}
	}
	on, err := s.stateDB.GetWorktreeInheritPermissions(r.Context())
	if err != nil {
		log.WithError(err).Warn("worktree: reading inherit-permissions setting")
		return inheritedRules{note: "reading setting: " + err.Error()}
	}
	if !on {
		return inheritedRules{}
	}
	// The /wt flow is OpenCode-only (AD-7) and doesn't pass ?platform=;
	// approvals are recorded under the platform id, so default to
	// "opencode" when no explicit hint is present.
	if platform == "" {
		platform = "opencode"
	}
	var reader permissions.LiveRuleReader
	if adapter, ok := s.registry.Get(platforms.ID(platform)); ok {
		reader = worktreeLiveRuleReader{adapter: adapter, ctx: r.Context()}
	}
	rules, count, err := permissions.BuildInheritedRulesWithLive(r.Context(), s.stateDB, reader, platform, parentSessionID)
	if err != nil {
		log.WithError(err).Warn("worktree: building inherited permission rules")
		return inheritedRules{note: "building rules: " + err.Error()}
	}
	return inheritedRules{platform: platform, rules: rules, count: count}
}

// applyInheritedPermissions applies built rules to the freshly-created
// worktree session. Returns the number of rules applied and a soft-fail
// note. Never blocks the launch.
func (s *Server) applyInheritedPermissions(r *http.Request, in inheritedRules, childSessionID string) (int, string) {
	if in.note != "" || in.count == 0 || childSessionID == "" {
		return 0, in.note
	}
	if err := s.sessions.SetPermissionRules(r.Context(), in.platform, platforms.SetPermissionRulesRequest{
		SessionID: childSessionID,
		Rules:     in.rules,
	}); err != nil {
		log.WithError(err).Warn("worktree: applying inherited permission rules")
		return 0, "applying rules: " + err.Error()
	}
	return in.count, ""
}

// worktreeLiveRuleReader adapts a platforms.Platform to the
// permissions.LiveRuleReader shape so a worktree child inherits the
// parent's live YOLO/custom posture, not just its recorded approvals.
type worktreeLiveRuleReader struct {
	adapter platforms.Platform
	ctx     context.Context
}

func (w worktreeLiveRuleReader) PermissionRules(_ string, sessionID string) ([]platforms.PermissionRule, error) {
	return w.adapter.PermissionRules(w.ctx, sessionID)
}

// archiveEmptySession archives the placeholder conversation a worktree
// child replaced. Archiving is non-destructive: the read may be stale and a
// message can land after it, so nothing is deleted. The archive marker stores
// the time_updated that was read, and any later activity unarchives the
// session, so a conversation written in concurrently resurfaces on its own.
func (s *Server) archiveEmptySession(ctx context.Context, platform, sessionID string) {
	if s.stateDB == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	adapter, ok := s.registry.Get(platforms.ID(platform))
	if !ok {
		return
	}
	detail, err := adapter.Session(ctx, sessionID, 1, 0)
	if err != nil || detail == nil || detail.Session == nil || detail.Session.MessageCount > 0 || detail.TotalMessages > 0 || len(detail.Messages) > 0 {
		return
	}
	// ponytail: the read time_updated, never clamped to now; a stale value only
	// makes the session easier to resurface.
	if err := s.stateDB.ArchiveSession(ctx, platform, sessionID, detail.Session.TimeUpdated); err != nil {
		log.WithError(err).WithField("session_id", sessionID).Warn("worktree: archiving empty parent session")
		return
	}
	s.broadcastSessionChanged(sessionID)
}
