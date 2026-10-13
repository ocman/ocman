package providerprotocol

import "strings"

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
