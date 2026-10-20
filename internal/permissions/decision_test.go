package permissions

import (
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestDecision(t *testing.T) {
	rules := []platforms.PermissionRule{
		{Permission: "bash", Pattern: "tea do *", Action: "allow"},
		{Permission: "bash", Pattern: "tea do delete*", Action: "ask"},
		{Permission: "bash", Pattern: "tea do delete all", Action: "deny"},
	}
	for _, tc := range []struct {
		permission string
		patterns   []string
		want       string
	}{
		{"bash", []string{"tea do something"}, "allow"},
		{"bash", []string{"tea do delete one"}, "ask"},
		{"bash", []string{"tea do something", "tea do delete all"}, "deny"},
		{"bash", []string{"tea do something", "unrelated"}, "ask"},
		{"edit", []string{"tea do something"}, ""},
		{"bash", nil, ""},
	} {
		if got := Decision(rules, tc.permission, tc.patterns); got != tc.want {
			t.Errorf("Decision(%q, %q) = %q, want %q", tc.permission, tc.patterns, got, tc.want)
		}
	}
}
