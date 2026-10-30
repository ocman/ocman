package pluginsmod

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/providerprotocol"
)

func TestSourceSessionRulesAllowLocationRelativeReadResources(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, ".source-inventory.json")
	if err := os.WriteFile(file, []byte("[]"), 0444); err != nil {
		t.Fatal(err)
	}
	rules, err := sourceSessionRules(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	// OpenCode V2 FileAccess.authorizeRead resolves even absolute in-location
	// input paths to location-relative resources before permission evaluation.
	for _, tc := range []struct{ resource, want string }{
		{file, "allow"}, {".source-inventory.json", "allow"},
		{"../.source-inventory.json", "deny"}, {"new-file.txt", "deny"},
		{filepath.Join(filepath.Dir(root), ".source-inventory.json"), "deny"},
		{"nested/.source-inventory.json", "deny"},
	} {
		action := "ask"
		for _, rule := range rules {
			if providerprotocol.MatchPattern(rule.Permission, "read") && providerprotocol.MatchPattern(rule.Pattern, tc.resource) {
				action = rule.Action
			}
		}
		if action != tc.want {
			t.Fatalf("inventory read resource %q: got %s want %s", tc.resource, action, tc.want)
		}
	}
	for _, permission := range []string{"edit", "shell", "external_directory", "grep", "mcp"} {
		for _, rule := range rules {
			if rule.Action == "allow" && providerprotocol.MatchPattern(rule.Permission, permission) {
				t.Fatalf("non-read tool %s allowed", permission)
			}
		}
	}
}
