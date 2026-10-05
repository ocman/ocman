package autoapprove

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

const sessionRulesReason = "allowed by the session's permission mode"

// ApplySessionRules approves a pending permission that the session's current
// permission rules allow. OpenCode reads a session's rules once per turn, so
// without this a mode change (e.g. to YOLO) leaves the running turn, and any
// subagent it started, prompting under the old rules. Returns whether it
// approved. Called when the rules change, for every prompt already pending.
func (s *Service) ApplySessionRules(ctx context.Context, platformID platforms.ID, adapter platforms.Platform, sessionID, permissionID, permission string, patterns []string, metadata map[string]any) bool {
	if !s.allowedBySessionRules(ctx, adapter, sessionID, permission, patterns) {
		return false
	}
	// A judge mid-run would otherwise answer the same prompt a second time.
	s.Cancel(sessionID, permissionID)
	asked := s.rememberAsked(string(platformID), sessionID, permissionID, permission, patterns, metadata)
	return s.approveBySessionRules(platformID, adapter, sessionID, permissionID, asked)
}

// approveBySessionRules answers the prompt at most once across the
// asked-event path and the rules-changed sweep.
func (s *Service) approveBySessionRules(platformID platforms.ID, adapter platforms.Platform, sessionID, permissionID string, asked askedPermission) bool {
	if !s.claimRulesApproval(sessionID, permissionID) {
		return false
	}
	logger := log.WithFields(log.Fields{"sessionID": sessionID, "permissionID": permissionID})
	logger.Debug("auto-approve: session permission rules allow this prompt")
	s.recordJudgedWithReasoning(sessionID, permissionID, verdictSafe, sessionRulesReason)
	s.respondAndPersistSafeApproval(platformID, adapter, sessionID, permissionID, asked, sessionRulesReason, logger)
	return true
}

// claimRulesApproval reports whether nothing has answered, or is answering,
// this prompt yet, and reserves it for the rules path.
func (s *Service) claimRulesApproval(sessionID, permissionID string) bool {
	key := autoApproveKey(sessionID, permissionID)
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	status := s.autoApprove[key]
	if status == nil {
		status = &autoApproveStatus{}
		s.autoApprove[key] = status
	}
	if status.rulesApproved || status.aiResponseInFlight || status.aiResponseSucceeded || status.manualResolvedAt > 0 {
		return false
	}
	status.rulesApproved = true
	return true
}

// allowedBySessionRules evaluates the prompt against the rules of its session
// and every ancestor, root first, so a subagent of a YOLO session is allowed
// too while its own rules (e.g. inherited denies) still win.
func (s *Service) allowedBySessionRules(ctx context.Context, adapter platforms.Platform, sessionID, permission string, patterns []string) bool {
	if adapter == nil || !adapter.Capabilities().PermissionRules {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	chain := []string{sessionID}
	for cur := sessionID; len(chain) <= maxParentWalk; {
		parent, ok := s.ResolveParentSessionID(ctx, cur)
		if !ok || parent == "" || parent == cur {
			break
		}
		chain = append(chain, parent)
		cur = parent
	}
	var rules []platforms.PermissionRule
	for i := len(chain) - 1; i >= 0; i-- {
		own, err := adapter.PermissionRules(ctx, chain[i])
		if err != nil {
			// Unknown rules must never widen access; fall through to the
			// judge or the user.
			return false
		}
		rules = append(rules, own...)
	}
	return permissions.Allows(rules, permission, patterns)
}
