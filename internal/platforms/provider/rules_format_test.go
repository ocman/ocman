package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenRulesRejectsUnsupportedApprovalFormats(t *testing.T) {
	for _, data := range []string{
		`{"approved":{"s":[{"permission":"bash","pattern":"echo *","action":"allow"}]}}`,
		`{"tentative":{"p":{"session":"s","rules":[{"permission":"edit","pattern":"/tmp/*","action":"allow"}]}}}`,
		`{"rules":{"s":[{"permission":"bash","pattern":"echo *","action":"allow"}]},"exactPatterns":true}`,
	} {
		t.Run(data, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "rules.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := OpenRules(path); err == nil {
				t.Fatal("unsupported raw approvals must fail closed, not become wildcard grants")
			}
		})
	}
}
