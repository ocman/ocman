package provider

import (
	"testing"
	"unicode/utf8"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// FuzzExactGrantMatchesOnlyItself checks the exact-pattern invariant: an
// "Allow always" grant recorded as providerprotocol.ExactPattern(granted)
// allows a prompt for ExactPattern(asked) only when asked == granted. It
// must hold for the adapter's own Rules.Decide, whether the grant is an
// approval or an inherited session rule, and for permissions.AllowsExact,
// which the autoapprove pipeline uses for the same prompts.
func FuzzExactGrantMatchesOnlyItself(f *testing.F) {
	for _, s := range [][2]string{
		{"cat *", "cat /etc/shadow"},
		{"rm foo?", "rm foo/x"},
		{"git status", "git status"},
		{`a\*`, `a\x`},
		{`a\`, `a\\`},
		{`\\*`, `\x`},
		{"git *", "git"},
		{"", "anything"},
		{"line\nbreak", "line\nbreak"},
	} {
		f.Add(s[0], s[1])
	}
	f.Fuzz(func(t *testing.T, granted, asked string) {
		// Prompts arrive as JSON, so patterns are valid UTF-8. Invalid bytes
		// never match (the regexp does not compile), which fails closed.
		if !utf8.ValidString(granted) || !utf8.ValidString(asked) {
			return
		}
		pattern := providerprotocol.ExactPattern(granted)
		if v := providerprotocol.PatternValue(pattern); v != granted {
			t.Fatalf("PatternValue(ExactPattern(%q)) = %q", granted, v)
		}
		want := granted == asked
		if got := providerprotocol.MatchPattern(pattern, asked); got != want && granted != "" {
			// The empty pattern is a flat permission and matches all.
			t.Fatalf("MatchPattern(ExactPattern(%q), %q) = %v", granted, asked, got)
		}
		if granted == "" {
			return
		}
		grant := []platforms.PermissionRule{{Permission: "bash", Pattern: pattern, Action: "allow"}}
		prompt := []string{providerprotocol.ExactPattern(asked)}
		if got := permissions.AllowsExact(grant, "bash", prompt); got != want {
			t.Fatalf("AllowsExact: grant %q, prompt %q allowed=%v", granted, asked, got)
		}
		r, err := OpenRules("")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.approve("approved", grant); err != nil {
			t.Fatal(err)
		}
		if err := r.set("inherited", grant); err != nil {
			t.Fatal(err)
		}
		wantDecision := map[bool]string{true: "once", false: ""}[want]
		for _, session := range []string{"approved", "inherited"} {
			decision := r.Decide(platforms.LivePrompt{"sessionID": session, "permission": "bash", "patterns": []any{prompt[0]}})
			if decision != wantDecision {
				t.Fatalf("Decide(%s): grant %q, prompt %q = %q, want %q", session, granted, asked, decision, wantDecision)
			}
		}
	})
}
