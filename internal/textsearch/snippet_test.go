package textsearch

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSnippetAndASCIIOnlyCaseFolding(t *testing.T) {
	if got := Lower("CAFÉ Test"); got != "cafÉ test" {
		t.Fatal(got)
	}
	for _, tc := range []struct{ text, needle, want string }{
		{"a\n b needle c", "needle", "a b needle c"},
		{"abc", "missing", "abc"},
		{strings.Repeat("é", 90) + "NEEDLE" + strings.Repeat("é", 90), "needle", "…" + strings.Repeat("é", 40) + "NEEDLE" + strings.Repeat("é", 40) + "…"},
	} {
		got := Snippet(tc.text, tc.needle)
		if got != tc.want || !utf8.ValidString(got) {
			t.Fatalf("%q want %q", got, tc.want)
		}
	}
}
