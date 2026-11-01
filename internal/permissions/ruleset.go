package permissions

import (
	"regexp"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// Matcher selects how rule and prompt patterns compare; see the package
// documentation.
type Matcher uint8

const (
	// GlobMatch follows OpenCode's Wildcard.match.
	GlobMatch Matcher = iota
	// ExactMatch follows providerprotocol's escapes.
	ExactMatch
)

// MatcherFor is the matcher of a platform with caps.
func MatcherFor(caps platforms.Capabilities) Matcher {
	if caps.ExactPatterns {
		return ExactMatch
	}
	return GlobMatch
}

// Rule actions and the empty decision.
const (
	ActionAllow = "allow"
	ActionAsk   = "ask"
	ActionDeny  = "deny"
	// ActionNone means no rule matched: the judge or the user decides.
	ActionNone = ""
)

// RuleSet is the input of ocman's permission evaluator; see the package
// documentation for its semantics.
type RuleSet struct {
	Matcher    Matcher
	Configured []platforms.PermissionRule
	Approved   []platforms.PermissionRule
	// Default is the action of a pattern no rule matches.
	Default string
}

// Decide returns ActionAllow, ActionAsk, ActionDeny or ActionNone for a
// prompt of permission with patterns.
func (s RuleSet) Decide(permission string, patterns []string) string {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	matched, allAllowed := false, true
	for _, pattern := range patterns {
		action := s.decidePattern(permission, pattern)
		if action == ActionDeny {
			return ActionDeny
		}
		matched = matched || action != ActionNone
		allAllowed = allAllowed && action == ActionAllow
	}
	switch {
	case allAllowed:
		return ActionAllow
	case !matched:
		return ActionNone
	}
	return ActionAsk
}

// Allows reports whether Decide allows the prompt.
func (s RuleSet) Allows(permission string, patterns []string) bool {
	return s.Decide(permission, patterns) == ActionAllow
}

func (s RuleSet) decidePattern(permission, pattern string) string {
	match := s.Matcher.match
	if s.Matcher == ExactMatch {
		pattern = providerprotocol.PatternValue(pattern)
	}
	action, ok := lastMatch(s.Configured, permission, pattern, match)
	if action == ActionDeny {
		return ActionDeny // approvals never override a configured deny
	}
	if approved, found := lastMatch(s.Approved, permission, pattern, match); found {
		action, ok = approved, true
	}
	if !ok {
		return s.Default
	}
	return action
}

// lastMatch is the action of the last rule matching permission and value,
// normalized so that only allow and deny keep their meaning.
func lastMatch(rules []platforms.PermissionRule, permission, value string, match func(pattern, s string) bool) (string, bool) {
	for i := len(rules) - 1; i >= 0; i-- {
		rule := rules[i]
		if match(rule.Permission, permission) && match(rule.Pattern, value) {
			if rule.Action == ActionAllow || rule.Action == ActionDeny {
				return rule.Action, true
			}
			return ActionAsk, true
		}
	}
	return ActionNone, false
}

func (m Matcher) match(pattern, s string) bool {
	if m == ExactMatch {
		return providerprotocol.MatchPattern(pattern, s)
	}
	return wildcardMatch(s, pattern)
}

// wildcardMatch mirrors OpenCode's Wildcard.match: `*` matches anything,
// `?` one character, backslashes compare as slashes, and a trailing " *"
// also matches the bare command ("git *" matches "git").
func wildcardMatch(str, pattern string) bool {
	str = strings.ReplaceAll(str, `\`, "/")
	pattern = strings.ReplaceAll(pattern, `\`, "/")
	var expr strings.Builder
	for _, r := range pattern {
		switch r {
		case '*':
			expr.WriteString(".*")
		case '?':
			expr.WriteString(".")
		default:
			expr.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	escaped := expr.String()
	if strings.HasSuffix(escaped, " .*") {
		escaped = strings.TrimSuffix(escaped, " .*") + "( .*)?"
	}
	re, err := regexp.Compile("(?s)^" + escaped + "$")
	return err == nil && re.MatchString(str)
}
