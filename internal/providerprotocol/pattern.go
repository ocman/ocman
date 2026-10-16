package providerprotocol

import (
	"regexp"
	"strings"
)

// Permission patterns use OpenCode's wildcard syntax (* and ?) plus one
// escape: a backslash before *, ? or another backslash makes that character
// literal; any other backslash is itself. A provider puts a tool call's
// command, path or URL into a prompt's patterns and always list as
// ExactPattern, so "Allow always" grants that exact call and stays exact
// when persisted and inherited, while intentional glob rules keep globbing.

// ExactPattern escapes s so that it matches only s.
func ExactPattern(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '*' || c == '?':
			b.WriteByte('\\')
		case c == '\\' && i+1 < len(s) && strings.IndexByte(`*?\`, s[i+1]) >= 0:
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MatchPattern is OpenCode's Wildcard.match with the escape above: * and ?
// glob, and a trailing " *" also matches the bare command. An empty pattern
// (flat permissions) matches all. A backslash escapes *, ? and itself, so an
// ExactPattern rule never globs.
func MatchPattern(pattern, s string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	var parts []string
	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case c == '\\' && i+1 < len(pattern) && strings.IndexByte(`*?\`, pattern[i+1]) >= 0:
			i++
			parts = append(parts, regexp.QuoteMeta(pattern[i:i+1]))
		case c == '*':
			parts = append(parts, ".*")
		case c == '?':
			parts = append(parts, ".")
		default:
			parts = append(parts, regexp.QuoteMeta(pattern[i:i+1]))
		}
	}
	if n := len(parts); n >= 2 && parts[n-1] == ".*" && parts[n-2] == " " {
		parts = append(parts[:n-2], "( .*)?")
	}
	ok, _ := regexp.MatchString("^(?s:"+strings.Join(parts, "")+")$", s)
	return ok
}

// PatternValue is the literal value an exact pattern stands for: the
// inverse of ExactPattern.
func PatternValue(p string) string {
	if !strings.Contains(p, `\`) {
		return p
	}
	var b strings.Builder
	for i := 0; i < len(p); i++ {
		if p[i] == '\\' && i+1 < len(p) && strings.IndexByte(`*?\`, p[i+1]) >= 0 {
			i++
		}
		b.WriteByte(p[i])
	}
	return b.String()
}
