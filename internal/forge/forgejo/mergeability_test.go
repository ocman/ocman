package forgejo

import (
	"encoding/json"
	"testing"
)

func TestPRMergeability(t *testing.T) {
	for _, value := range []string{"true", "false", "null"} {
		t.Run(value, func(t *testing.T) {
			var raw fjPR
			if err := json.Unmarshal([]byte(`{"mergeable":`+value+`}`), &raw); err != nil {
				t.Fatal(err)
			}
			pr := raw.toForge("forge.example", "owner/repo")
			if value == "null" {
				if pr.Mergeable != nil {
					t.Fatal("unknown mergeability must remain nil")
				}
			} else if pr.Mergeable == nil || *pr.Mergeable != (value == "true") {
				t.Fatalf("mergeability not preserved: %v", pr.Mergeable)
			}
		})
	}
}
