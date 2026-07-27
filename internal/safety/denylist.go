// Package safety holds the hard command denylist shared by the
// auto-approve judge and the workflow command executor.
//
// Both of those gates are otherwise controlled by untrusted input: the
// judge's only gate is an LLM verdict on a prompt that interpolates the
// agent's own command text, and a workflow command node is gated only
// by permission rules that come from the agent-published workflow
// definition itself (an agent can publish `bash *: allow`). This
// denylist is evaluated first in both paths and cannot be overridden by
// a rule, a verdict, or a cached approval.
//
// It is deliberately small and obvious. It is a backstop against the
// catastrophic and irreversible, not a sandbox: anything not listed
// still goes to the normal gate. A false positive costs the user one
// manual approval, which is the correct direction to fail.
package safety

import "regexp"

// Rule is one hard-denied command shape.
type Rule struct {
	// Name is the short, stable reason reported to the user and logs.
	Name string
	// Pattern matches against the command text (and, for the judge,
	// against string-valued tool metadata such as file paths).
	Pattern *regexp.Regexp
}

// Rules is the denylist. Ordered most-specific first only for the sake
// of readable reasons; every rule is checked until one matches.
//
// ponytail: a flat regex table, not a shell parser. It can be evaded by
// a determined agent (base64, variable indirection, `r''m`). Upgrade
// path if that matters: parse the command with a real shell lexer and
// match on argv, or drop privileges via a sandbox.
var Rules = []Rule{
	{"sudo / privilege escalation", regexp.MustCompile(`(?i)(^|[\s;&|(])(sudo|doas|su)\s`)},
	{"pipe to shell", regexp.MustCompile(`(?i)\|\s*(sudo\s+)?(ba|z|k|da|)sh\b`)},
	{"recursive or forced delete", regexp.MustCompile(`(?i)(^|[\s;&|(])rm\s+(-{1,2}\S+\s+)*-{1,2}\S*[rf]`)},
	{"disk or device write", regexp.MustCompile(`(?i)(^|[\s;&|(])(mkfs\S*|shred|fdisk)\s|(^|[\s;&|(])dd\s[^|;&]*\bof=/dev/`)},
	{"credential or key path", regexp.MustCompile(`(?i)(\.ssh/|\.gnupg/|\.aws/credentials|\bid_rsa\b|\bid_ed25519\b|\.pem\b|\.env\b|\.netrc\b|\.npmrc\b|\.pypirc\b|\bcredentials\.json\b)`)},
	{"force push", regexp.MustCompile(`(?i)(^|[\s;&|(])git\s+push\b[^;&|]*(--force|\s-f(\s|$))`)},
	{"package publish", regexp.MustCompile(`(?i)(^|[\s;&|(])((npm|pnpm|yarn|poetry|cargo)\s+publish|twine\s+upload|gem\s+push|docker\s+push)\b`)},
}

// Denied returns the name of the first denylist rule that text matches,
// or "" when nothing matches. Callers must treat a non-empty result as
// a hard refusal: no LLM verdict, permission rule, or cached approval
// may override it.
func Denied(text string) string {
	for _, rule := range Rules {
		if rule.Pattern.MatchString(text) {
			return rule.Name
		}
	}
	return ""
}

// DeniedAny returns the first rule name matched by any of the supplied
// strings. Convenience for callers that have several untrusted fields
// (a command plus a file path, say) and want one verdict.
func DeniedAny(texts ...string) string {
	for _, text := range texts {
		if name := Denied(text); name != "" {
			return name
		}
	}
	return ""
}
