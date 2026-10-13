package provider

import (
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
	log "github.com/sirupsen/logrus"
)

// escapeLegacyLocked turns the raw patterns of approvals saved before
// exact patterns into exact ones: each was a prompt's literal tool
// argument, so `echo *` approved that command, not every echo. An approval
// already escaped by an earlier build only narrows further.
func (r *Rules) escapeLegacyLocked() {
	escape := func(rules []platforms.PermissionRule) {
		for i := range rules {
			rules[i].Pattern = providerprotocol.ExactPattern(rules[i].Pattern)
		}
	}
	for _, rules := range r.data.Approved {
		escape(rules)
	}
	for _, t := range r.data.Tentative {
		escape(t.Rules)
	}
	r.data.ExactPatterns = true
}

// legacyFlatKeys were inherited as one pattern-less allow before Claude
// approvals were exact (permissions.exactScopePlatforms).
var legacyFlatKeys = map[string]bool{"webfetch": true, "websearch": true}

// EscapeInheritedApprovals converts, once, the approvals a session's
// ruleset inherited before patterns were exact. approvals are every
// recorded approval of this provider (auto_approved_permission, the only
// source inheritance reads besides a parent's ruleset). Such a rule is an
// allow whose pattern is an approval's literal value, `echo *` approving
// that one command, so it is escaped; an allow-all for a flat key that was
// approved per URL or query is dropped, so the child is asked again. A
// user-written allow can coincide with an approval and is narrowed the
// same way: either way the result never grants more than before, and a
// deny is never touched. An unwritable store keeps the converted copy in
// memory; the next successful write records it.
func (r *Rules) EscapeInheritedApprovals(approvals []state.ApprovedPermission) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.data.ExactRules {
		return
	}
	literal := map[platforms.PermissionRule]bool{}
	for _, a := range approvals {
		key, _, _ := strings.Cut(a.PermissionText, " ")
		literal[platforms.PermissionRule{Permission: key}] = true
		for _, p := range a.Patterns {
			literal[platforms.PermissionRule{Permission: key, Pattern: providerprotocol.PatternValue(p)}] = true
		}
	}
	convert := func() {
		for id, rules := range r.data.Rules {
			out := make([]platforms.PermissionRule, 0, len(rules))
			for _, rule := range rules {
				if rule.Action == "allow" {
					approved := literal[platforms.PermissionRule{Permission: rule.Permission, Pattern: rule.Pattern}]
					if rule.Pattern == "" && legacyFlatKeys[rule.Permission] && approved {
						continue
					}
					if approved {
						rule.Pattern = providerprotocol.ExactPattern(rule.Pattern)
					}
				}
				out = append(out, rule)
			}
			if len(out) == 0 {
				delete(r.data.Rules, id)
			} else {
				r.data.Rules[id] = out
			}
		}
		r.data.ExactRules = true
	}
	if err := r.commitLocked(convert); err != nil {
		convert()
		log.WithError(err).Warn("provider: recording exact inherited permission rules")
	}
}
