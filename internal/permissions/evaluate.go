package permissions

import (
	"regexp"
	"strings"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// Allows reports whether rules allow every pattern of a permission prompt,
// using OpenCode's own semantics: the last matching rule wins and no match
// means "ask". OpenCode reads a session's rules once per turn, so ocman uses
// this to apply a mid-turn rules change (e.g. switching to YOLO) to prompts
// that turn still raises.
func Allows(rules []platforms.PermissionRule, permission string, patterns []string) bool {
	return allows(rules, permission, patterns, wildcardMatch, func(p string) string { return p })
}

// AllowsExact is Allows for platforms whose patterns use providerprotocol's
// escapes (Capabilities.ExactPatterns), matching like the provider adapter's
// own rules: a prompt pattern stands for its literal value, and an escaped
// grant such as `cat \*` matches only `cat *`, never `cat /etc/shadow`.
func AllowsExact(rules []platforms.PermissionRule, permission string, patterns []string) bool {
	match := func(str, pattern string) bool { return providerprotocol.MatchPattern(pattern, str) }
	return allows(rules, permission, patterns, match, providerprotocol.PatternValue)
}

func allows(rules []platforms.PermissionRule, permission string, patterns []string, match func(str, pattern string) bool, value func(string) string) bool {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	for _, pattern := range patterns {
		if evaluate(rules, permission, value(pattern), match) != "allow" {
			return false
		}
	}
	return true
}

func evaluate(rules []platforms.PermissionRule, permission, pattern string, match func(str, pattern string) bool) string {
	for i := len(rules) - 1; i >= 0; i-- {
		if match(permission, rules[i].Permission) && match(pattern, rules[i].Pattern) {
			return rules[i].Action
		}
	}
	return "ask"
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
