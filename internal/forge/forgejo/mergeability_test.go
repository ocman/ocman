package forgejo

import (
	"encoding/json"
	"testing"

	"github.com/ocman/ocman/internal/forge/forgehttp"
)

func TestPRMergeability(t *testing.T) {
	for _, value := range []string{"true", "false", "null"} {
		t.Run(value, func(t *testing.T) {
			var raw forgehttp.PR
			if err := json.Unmarshal([]byte(`{"mergeable":`+value+`}`), &raw); err != nil {
				t.Fatal(err)
			}
			pr := toPR(raw, "forge.example", "owner/repo")
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
