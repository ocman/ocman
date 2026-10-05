package permissions

import (
	"regexp"
	"strings"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// Allows reports whether rules allow every pattern of a permission prompt,
// using OpenCode's own semantics: the last matching rule wins and no match
// means "ask". OpenCode reads a session's rules once per turn, so ocman uses
// this to apply a mid-turn rules change (e.g. switching to YOLO) to prompts
// that turn still raises.
func Allows(rules []platforms.PermissionRule, permission string, patterns []string) bool {
	if len(patterns) == 0 {
		patterns = []string{"*"}
	}
	for _, pattern := range patterns {
		if evaluate(rules, permission, pattern) != "allow" {
			return false
		}
	}
	return true
}

func evaluate(rules []platforms.PermissionRule, permission, pattern string) string {
	for i := len(rules) - 1; i >= 0; i-- {
		if wildcardMatch(permission, rules[i].Permission) && wildcardMatch(pattern, rules[i].Pattern) {
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
