package opencode

import (
	"unicode/utf8"
)

// truncateUTF8 cuts s to at most n bytes without splitting a multi-byte
// character: a rune straddling the limit is dropped whole. It backs off at
// most utf8.UTFMax-1 bytes, so input that is already invalid is still cut.
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for i := n; i > n-utf8.UTFMax && i > 0; i-- {
		if utf8.RuneStart(s[i]) {
			return s[:i]
		}
	}
	return s[:n]
}
