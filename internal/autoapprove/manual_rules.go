package autoapprove

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
)

func manualRulesKey(platform, sessionID string) string {
	return "session.permissions." + base64.RawURLEncoding.EncodeToString([]byte(platform+"\x00"+sessionID))
}

func literalPatterns(patterns []string) []string {
	out := make([]string, len(patterns))
	for i, pattern := range patterns {
		out[i] = providerprotocol.PatternValue(pattern)
	}
	return out
}

func (s *Service) manualRules(ctx context.Context, platform, sessionID string) ([]platforms.PermissionRule, error) {
	if s.deps.Store == nil {
		return nil, nil
	}
	value, exists, err := s.deps.Store.GetSetting(ctx, manualRulesKey(platform, sessionID))
	if err != nil || !exists {
		return nil, err
	}
	var rules []platforms.PermissionRule
	err = json.Unmarshal([]byte(value), &rules)
	if err == nil {
		err = validateManualRules(rules)
	}
	return rules, err
}

// manualDecision reads the current session and its nested-subagent ancestors.
// Rules never enter the platform ruleset or the inheritable approval cache.
func (s *Service) manualDecision(ctx context.Context, adapter platforms.Platform, sessionID string, asked askedPermission) (string, error) {
	if adapter == nil || s.deps.Store == nil || isRemote(adapter.ID()) {
		return "", nil // Only the owner evaluates policy.
	}
	chain := []string{sessionID}
	seen := map[string]bool{sessionID: true}
	for cur := sessionID; len(chain) <= maxParentWalk; {
		parent, ok := s.ResolveParentSessionID(ctx, cur)
		if s.deps.PermissionParent != nil {
			var err error
			parent, err = s.deps.PermissionParent(ctx, adapter, cur)
			if err != nil {
				return "", err
			}
			ok = parent != ""
		}
		if !ok || parent == "" {
			break
		}
		if seen[parent] {
			return "", fmt.Errorf("cyclic session ancestry")
		}
		seen[parent] = true
		chain = append(chain, parent)
		cur = parent
	}
	if len(chain) > maxParentWalk {
		return "", fmt.Errorf("session ancestry exceeds limit")
	}
	var rules []platforms.PermissionRule
	for i := len(chain) - 1; i >= 0; i-- {
		own, err := s.manualRules(ctx, string(adapter.ID()), chain[i])
		if err != nil {
			return "", err
		}
		rules = append(rules, own...)
	}
	patterns := append([]string(nil), asked.patterns...)
	if adapter.Capabilities().ExactPatterns {
		for i := range patterns {
			patterns[i] = providerprotocol.PatternValue(patterns[i])
		}
	}
	return permissions.Decision(rules, asked.permission, patterns), nil
}

func (s *Service) applyManualRules(ctx context.Context, adapter platforms.Platform, sessionID, permissionID string, asked askedPermission) bool {
	s.manualRulesMu.RLock()
	defer s.manualRulesMu.RUnlock()
	action, err := s.manualDecision(ctx, adapter, sessionID, asked)
	if err != nil {
		s.handleUnsafeVerdict(adapter.ID(), sessionID, permissionID, JudgeResult{Verdict: verdictNeedsUser, Reasoning: "Could not read session permission rules; review this request manually"})
		return true
	}
	if action == "" {
		return false
	}
	s.applyManualAction(ctx, adapter, sessionID, permissionID, action)
	return true
}

func (s *Service) applyManualAction(ctx context.Context, adapter platforms.Platform, sessionID, permissionID, action string) {
	if action == "ask" {
		s.handleUnsafeVerdict(adapter.ID(), sessionID, permissionID, JudgeResult{Verdict: verdictNeedsUser, Reasoning: "Your session rule requires manual approval"})
		return
	}
	reply := "once"
	if action == "deny" {
		reply = "reject"
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if reporter, ok := adapter.(platforms.ReplyOutcomeReporter); ok && !reporter.ReplyOutcomes(ctx, sessionID) {
		s.handleUnsafeVerdict(adapter.ID(), sessionID, permissionID, JudgeResult{Verdict: verdictNeedsUser, Reasoning: "Your session's provider cannot confirm automatic replies; review this request manually"})
		return
	}
	if !s.claimManualResponse(sessionID, permissionID) {
		return
	}
	s.beginAIResponse(sessionID, permissionID, reply)
	err := adapter.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: sessionID, PermissionID: permissionID, Reply: reply})
	if err != nil && !errors.Is(err, platforms.ErrReplyUnrecorded) {
		s.finishAIResponse(sessionID, permissionID, false)
		s.handleUnsafeVerdict(adapter.ID(), sessionID, permissionID, JudgeResult{Verdict: verdictNeedsUser, Reasoning: "Could not apply your session rule; review this request manually"})
		return
	}
	if !s.finishAIResponse(sessionID, permissionID, true) {
		return
	}
	s.recordJudgedWithReasoning(sessionID, permissionID, "session-rule-resolved", "Handled by your session "+action+" rule")
	if s.deps.Store != nil {
		_ = s.deps.Store.UpsertPermissionLifecycle(ctx, state.PermissionLifecycle{Platform: string(adapter.ID()), SessionID: sessionID, PermissionID: permissionID, ResolvedAt: time.Now().UnixMilli(), Resolution: state.PermissionResolution("rule-" + action)})
	}
	if store, ok := s.deps.Store.(interface {
		ResolvePermissionInboxItem(context.Context, string, string, string) error
	}); ok {
		_ = store.ResolvePermissionInboxItem(ctx, string(adapter.ID()), sessionID, permissionID)
	}
	s.broadcastPermissionResolved(sessionID, permissionID, "session-rule-"+action)
}

func (s *Service) claimManualResponse(sessionID, permissionID string) bool {
	s.autoApproveMu.Lock()
	defer s.autoApproveMu.Unlock()
	if s.autoApprove == nil {
		s.autoApprove = make(map[string]*autoApproveStatus)
	}
	key := autoApproveKey(sessionID, permissionID)
	st := s.autoApprove[key]
	if st == nil {
		st = &autoApproveStatus{}
		s.autoApprove[key] = st
	}
	if st.manualRuleResponse || st.aiResponseInFlight || st.aiResponseSucceeded || st.manualResolvedAt > 0 {
		return false
	}
	st.manualRuleResponse = true
	return true
}

// guardManualApproval serializes rule saves with the final automatic reply.
// A judge that started before an ask/deny edit cannot approve afterward.
func (s *Service) guardManualApproval(ctx context.Context, adapter platforms.Platform, sessionID, permissionID string, asked askedPermission) (func(), bool) {
	s.manualRulesMu.RLock()
	action, err := s.manualDecision(ctx, adapter, sessionID, asked)
	if err == nil && action == "" {
		return s.manualRulesMu.RUnlock, true
	}
	s.manualRulesMu.RUnlock()
	s.applyManualRules(ctx, adapter, sessionID, permissionID, asked)
	return func() {}, false
}
