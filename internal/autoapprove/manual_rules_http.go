package autoapprove

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/NoUseFreak/ocman/internal/httpx"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// PermissionPolicy is Ocman's own session policy, never native agent settings.
type PermissionPolicy struct {
	Rules    []platforms.PermissionRule     `json:"rules"`
	Requests []state.PermissionHistoryEntry `json:"requests"`
}

// PolicyOwner is implemented by remote platform adapters. Policy is evaluated
// and persisted only on the machine that owns the session.
type PolicyOwner interface {
	SessionPermissionPolicy(context.Context, string, *[]platforms.PermissionRule) (*PermissionPolicy, error)
}

func validateManualRules(rules []platforms.PermissionRule) error {
	if len(rules) > 200 {
		return fmt.Errorf("at most 200 session rules are allowed")
	}
	for _, rule := range rules {
		if strings.TrimSpace(rule.Permission) == "" || strings.TrimSpace(rule.Pattern) == "" || len(rule.Permission) > 256 || len(rule.Pattern) > 4096 {
			return fmt.Errorf("each rule needs a permission and pattern of at most 256 and 4096 characters")
		}
		if rule.Action != "allow" && rule.Action != "ask" && rule.Action != "deny" {
			return fmt.Errorf("action must be allow, ask or deny")
		}
	}
	return nil
}

func (m *Module) handleManualPolicy(w http.ResponseWriter, r *http.Request, id string, adapter platforms.Platform) {
	var rules *[]platforms.PermissionRule
	if r.Method == http.MethodPut {
		var body struct {
			Rules []platforms.PermissionRule `json:"rules"`
		}
		if !httpx.ReadJSON(w, r, httpx.MaxRequestBody, &body) {
			return
		}
		if err := validateManualRules(body.Rules); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		rules = &body.Rules
	}
	policy, err := m.SessionPermissionPolicy(r.Context(), adapter, id, rules)
	if err != nil {
		httpx.WritePlatformError(w, "session permission policy", err)
		return
	}
	httpx.WriteJSON(w, policy)
}

// SessionPermissionPolicy is shared by REST and authenticated owner RPCs.
func (m *Module) SessionPermissionPolicy(ctx context.Context, adapter platforms.Platform, id string, rules *[]platforms.PermissionRule) (*PermissionPolicy, error) {
	if owner, ok := adapter.(PolicyOwner); ok {
		return owner.SessionPermissionPolicy(ctx, id, rules)
	}
	if isRemote(adapter.ID()) {
		return nil, platforms.ErrUnsupported
	}
	if m.Store == nil {
		return nil, fmt.Errorf("state database unavailable")
	}
	detail, err := adapter.Session(ctx, id, 1, 0)
	if err != nil {
		return nil, err
	}
	if detail == nil || detail.Session == nil {
		return nil, platforms.ErrNotFound
	}
	if rules != nil {
		if err := validateManualRules(*rules); err != nil {
			return nil, err
		}
		value, err := json.Marshal(*rules)
		if err != nil {
			return nil, err
		}
		m.svc.manualRulesMu.Lock()
		err = m.Store.SetSetting(ctx, manualRulesKey(string(adapter.ID()), id), string(value))
		m.svc.manualRulesMu.Unlock()
		if err != nil {
			return nil, err
		}
	}
	current, err := m.svc.manualRules(ctx, string(adapter.ID()), id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		current = []platforms.PermissionRule{}
	}
	ids := []string{id}
	if detail != nil {
		included := map[string]bool{id: true}
		for pass := 0; pass < len(detail.SessionTree); pass++ {
			changed := false
			for _, child := range detail.SessionTree {
				if !included[child.ID] && included[child.ParentID] {
					included[child.ID] = true
					ids = append(ids, child.ID)
					changed = true
				}
			}
			if !changed {
				break
			}
		}
	}
	if rules != nil {
		for _, sessionID := range ids {
			m.applyManualToPending(ctx, adapter, sessionID)
		}
	}
	history, err := m.Store.SessionPermissionHistory(ctx, string(adapter.ID()), ids)
	if err != nil {
		return nil, err
	}
	if adapter.Capabilities().ExactPatterns {
		for i := range history {
			history[i].Patterns = literalPatterns(history[i].Patterns)
		}
	}
	return &PermissionPolicy{Rules: current, Requests: history}, nil
}

func (m *Module) applyManualToPending(ctx context.Context, adapter platforms.Platform, id string) {
	entries, err := adapter.ListPermissions(ctx, id)
	if err != nil {
		return
	} // The saved rules still cover future requests.
	m.eachPermission(entries, id, func(sid, pid, permission string, entry platforms.LivePrompt) {
		key, err := platforms.PermissionNotificationKey(ctx, adapter, string(adapter.ID()), sid, pid)
		if err != nil {
			return
		}
		_ = m.Store.EnsurePermissionInboxItem(ctx, state.InboxPermission{ItemID: key, Platform: string(adapter.ID()), SessionID: sid, PermissionID: pid, Permission: permission, Patterns: PromptPatterns(entry), Always: promptStrings(entry["always"]), Metadata: PromptMetadata(entry)})
		asked := m.svc.rememberAsked(string(adapter.ID()), sid, pid, permission, PromptPatterns(entry), PromptMetadata(entry))
		if m.svc.applyManualRules(ctx, adapter, sid, pid, asked) {
			m.svc.Cancel(sid, pid)
		} else {
			m.svc.resumeAfterManualRule(adapter, sid, pid, asked)
		}
	})
}
