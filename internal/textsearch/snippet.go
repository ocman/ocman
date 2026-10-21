// Package textsearch shares literal search and snippet rules across retained
// history and native integration diagnostics.
package textsearch

import (
	"strings"
	"unicode/utf8"
)

// Lower mirrors SQLite lower: ASCII only, preserving byte offsets.
func Lower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// Snippet bounds surrounding prose without splitting UTF-8 runes.
func Snippet(text, needle string) string {
	i := max(strings.Index(Lower(text), needle), 0)
	start, end := max(i-80, 0), min(i+len(needle)+80, len(text))
	for start > 0 && !utf8.RuneStart(text[start]) {
		start--
	}
	for end < len(text) && !utf8.RuneStart(text[end]) {
		end++
	}
	out := strings.Join(strings.Fields(text[start:end]), " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	return out
}
