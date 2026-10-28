package permissions

import (
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// Decision evaluates Ocman's manual rules. An empty decision means no rule
// matched. The last match wins per resource; a denial of any resource denies
// the whole request, and allowing only some resources still requires a user.
func Decision(rules []platforms.PermissionRule, permission string, patterns []string) string {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	matched, allAllowed := false, true
	for _, pattern := range patterns {
		action := ""
		for i := len(rules) - 1; i >= 0; i-- {
			rule := rules[i]
			if providerprotocol.MatchPattern(rule.Permission, permission) && providerprotocol.MatchPattern(rule.Pattern, pattern) {
				action = rule.Action
				break
			}
		}
		if action == "deny" {
			return "deny"
		}
		matched = matched || action != ""
		allAllowed = allAllowed && action == "allow"
	}
	if !matched {
		return ""
	}
	if allAllowed {
		return "allow"
	}
	return "ask"
}
