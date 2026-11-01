package permissions_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/permissions"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// semanticsCase is one rule set evaluated by every ocman permission
// evaluator. want is the unified decision: "allow", "ask", "deny", or ""
// when no rule matched and the judge or the user decides.
type semanticsCase struct {
	name       string
	exact      bool // Capabilities.ExactPatterns
	configured []platforms.PermissionRule
	approved   []platforms.PermissionRule
	preflight  bool
	permission string
	patterns   []string
	want       string
}

func rule(permission, pattern, action string) platforms.PermissionRule {
	return platforms.PermissionRule{Permission: permission, Pattern: pattern, Action: action}
}

func bash(pattern, action string) platforms.PermissionRule { return rule("bash", pattern, action) }

func semanticsCases() []semanticsCase {
	x := providerprotocol.ExactPattern
	return []semanticsCase{
		// Empty rule sets leave the decision to the judge or the user.
		{name: "empty rule set exact", exact: true, permission: "bash", patterns: []string{x("ls")}, want: ""},
		{name: "empty rule set glob", permission: "bash", patterns: []string{"ls"}, want: ""},
		{name: "empty rule set without patterns", exact: true, permission: "webfetch", want: ""},
		{name: "unrelated permission", exact: true, configured: []platforms.PermissionRule{rule("edit", "*", "allow")}, permission: "bash", patterns: []string{"ls"}, want: ""},

		// Preflight prompts only exist on the provider adapter: with no ocman
		// restriction they continue to the native checks, but rules still apply.
		{name: "preflight without rules allows", exact: true, preflight: true, permission: "bash", patterns: []string{"ls"}, want: "allow"},
		{name: "preflight honours a configured deny", exact: true, preflight: true, configured: []platforms.PermissionRule{bash("rm *", "deny")}, permission: "bash", patterns: []string{"rm x"}, want: "deny"},
		{name: "preflight honours a configured ask", exact: true, preflight: true, configured: []platforms.PermissionRule{bash("*", "ask")}, permission: "bash", patterns: []string{"ls"}, want: "ask"},

		// A configured deny always beats an approved allow.
		{name: "approved allow after configured deny exact", exact: true, configured: []platforms.PermissionRule{bash("rm *", "deny")}, approved: []platforms.PermissionRule{bash(x("rm x"), "allow")}, permission: "bash", patterns: []string{x("rm x")}, want: "deny"},
		{name: "approved allow after configured deny glob", configured: []platforms.PermissionRule{bash("rm *", "deny")}, approved: []platforms.PermissionRule{bash("rm x", "allow")}, permission: "bash", patterns: []string{"rm x"}, want: "deny"},
		{name: "approved allow after configured wildcard deny", exact: true, configured: []platforms.PermissionRule{rule("*", "*", "deny")}, approved: []platforms.PermissionRule{rule("*", "*", "allow")}, permission: "edit", patterns: []string{"a.go"}, want: "deny"},
		{name: "approved allow satisfies a configured ask", exact: true, configured: []platforms.PermissionRule{bash("*", "ask")}, approved: []platforms.PermissionRule{bash("ls", "allow")}, permission: "bash", patterns: []string{"ls"}, want: "allow"},
		{name: "approved allow only covers its patterns", exact: true, configured: []platforms.PermissionRule{bash("rm *", "deny")}, approved: []platforms.PermissionRule{bash("ls", "allow")}, permission: "bash", patterns: []string{"ls"}, want: "allow"},

		// Within a tier the last matching rule wins.
		{name: "later configured allow overrides deny", exact: true, configured: []platforms.PermissionRule{bash("rm *", "deny"), bash("rm x", "allow")}, permission: "bash", patterns: []string{"rm x"}, want: "allow"},
		{name: "later narrower deny wins", configured: []platforms.PermissionRule{bash("git *", "allow"), bash("git push*", "deny")}, permission: "bash", patterns: []string{"git push origin"}, want: "deny"},
		{name: "later broader allow wins", configured: []platforms.PermissionRule{bash("git push*", "deny"), bash("git *", "allow")}, permission: "bash", patterns: []string{"git push origin"}, want: "allow"},
		{name: "later ask wins over allow", exact: true, configured: []platforms.PermissionRule{rule("*", "*", "allow"), bash("*", "ask")}, permission: "bash", patterns: []string{"ls"}, want: "ask"},

		// Across patterns any deny denies and only all-allow allows.
		{name: "every pattern must allow", exact: true, configured: []platforms.PermissionRule{bash("git *", "allow")}, permission: "bash", patterns: []string{"git status", "rm x"}, want: "ask"},
		{name: "any deny denies", configured: []platforms.PermissionRule{bash("git *", "allow"), bash("rm *", "deny")}, permission: "bash", patterns: []string{"git status", "rm x"}, want: "deny"},
		{name: "wildcard permission without patterns", configured: []platforms.PermissionRule{rule("*", "*", "allow")}, permission: "webfetch", want: "allow"},

		// Exact platforms: a prompt pattern stands for its literal value and an
		// escaped grant never globs.
		{name: "exact escaped grant does not glob", exact: true, configured: []platforms.PermissionRule{bash(x("cat *"), "allow")}, permission: "bash", patterns: []string{x("cat /etc/shadow")}, want: ""},
		{name: "exact escaped grant allows itself", exact: true, configured: []platforms.PermissionRule{bash(x("cat *"), "allow")}, permission: "bash", patterns: []string{x("cat *")}, want: "allow"},
		{name: "exact glob rule globs", exact: true, configured: []platforms.PermissionRule{bash("git *", "allow")}, permission: "bash", patterns: []string{x("git status"), "git"}, want: "allow"},

		// Glob platforms follow OpenCode: a backslash reads as "/".
		{name: "glob backslash reads as slash", configured: []platforms.PermissionRule{rule("read", `C:\src\*`, "allow")}, permission: "read", patterns: []string{"C:/src/a"}, want: "allow"},
		{name: "glob escaped deny follows OpenCode", configured: []platforms.PermissionRule{bash(`cat \*`, "deny")}, permission: "bash", patterns: []string{"cat /etc/shadow"}, want: "deny"},
		{name: "glob backslash is not an escape", configured: []platforms.PermissionRule{bash(`cat \*`, "allow")}, permission: "bash", patterns: []string{"cat *"}, want: ""},
		{name: "glob trailing space-star matches bare command", configured: []platforms.PermissionRule{bash("git *", "allow")}, permission: "bash", patterns: []string{"git"}, want: "allow"},
		{name: "glob regex characters are literal", configured: []platforms.PermissionRule{rule("read", "a.(b)", "allow")}, permission: "read", patterns: []string{"axxb"}, want: ""},
	}
}

// evaluators are ocman's permission evaluators, each called the way its
// production caller calls it.
var evaluators = []struct {
	name string
	// applies reports whether the evaluator sees prompts like tc.
	applies func(tc semanticsCase) bool
	eval    func(t *testing.T, tc semanticsCase) string
}{
	{
		// The provider adapter answers prompts itself; its patterns are exact.
		name:    "provider.Rules.Decide",
		applies: func(tc semanticsCase) bool { return tc.exact },
		eval:    decideByProviderRules,
	},
	{
		// autoapprove's manual session rules (manualDecision).
		name:    "autoapprove manual rules",
		applies: func(tc semanticsCase) bool { return !tc.preflight },
		eval: func(_ *testing.T, tc semanticsCase) string {
			patterns := append([]string(nil), tc.patterns...)
			if tc.exact {
				for i := range patterns {
					patterns[i] = providerprotocol.PatternValue(patterns[i])
				}
			}
			return permissions.Decision(tierless(tc), tc.permission, patterns)
		},
	},
	{
		// autoapprove's session-rules sweep (allowedBySessionRules) only
		// distinguishes allow from everything else.
		name:    "autoapprove session rules",
		applies: func(tc semanticsCase) bool { return !tc.preflight },
		eval: func(_ *testing.T, tc semanticsCase) string {
			allows := permissions.Allows
			if tc.exact {
				allows = permissions.AllowsExact
			}
			if allows(tierless(tc), tc.permission, tc.patterns) {
				return "allow"
			}
			return "not allow"
		},
	},
}

// tierless is the rule list an evaluator without a configured/approved
// boundary receives: approvals after the configured rules.
func tierless(tc semanticsCase) []platforms.PermissionRule {
	return append(append([]platforms.PermissionRule(nil), tc.configured...), tc.approved...)
}

func decideByProviderRules(t *testing.T, tc semanticsCase) string {
	t.Helper()
	var store struct {
		Rules         map[string][]platforms.PermissionRule `json:"rules"`
		Approved      map[string][]platforms.PermissionRule `json:"approved"`
		ExactPatterns bool                                  `json:"exactPatterns"`
		ExactRules    bool                                  `json:"exactRules"`
	}
	store.Rules = map[string][]platforms.PermissionRule{"s": tc.configured}
	store.Approved = map[string][]platforms.PermissionRule{"s": tc.approved}
	store.ExactPatterns, store.ExactRules = true, true
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := provider.OpenRules(path)
	if err != nil {
		t.Fatal(err)
	}
	prompt := platforms.LivePrompt{"sessionID": "s", "permission": tc.permission}
	if tc.patterns != nil {
		list := make([]any, len(tc.patterns))
		for i, p := range tc.patterns {
			list[i] = p
		}
		prompt["patterns"] = list
	}
	if tc.preflight {
		prompt["metadata"] = map[string]any{"ocmanPreflight": true}
	}
	switch r.Decide(prompt) {
	case "once":
		return "allow"
	case "reject":
		return "deny"
	}
	return "ask or none"
}

// normalize maps the unified decision onto what an evaluator can express.
func normalize(evaluator, want string) string {
	switch evaluator {
	case "provider.Rules.Decide":
		if want != "allow" && want != "deny" {
			return "ask or none"
		}
	case "autoapprove session rules":
		if want != "allow" {
			return "not allow"
		}
	}
	return want
}

// TestEvaluatorsAgree documents the semantics every evaluator must share,
// so a rule set never denies in one and allows in another.
func TestEvaluatorsAgree(t *testing.T) {
	for _, tc := range semanticsCases() {
		for _, ev := range evaluators {
			if !ev.applies(tc) {
				continue
			}
			t.Run(tc.name+"/"+ev.name, func(t *testing.T) {
				if got, want := ev.eval(t, tc), normalize(ev.name, tc.want); got != want {
					t.Errorf("configured %v, approved %v, %s %q: got %q, want %q", tc.configured, tc.approved, tc.permission, tc.patterns, got, want)
				}
			})
		}
	}
}
