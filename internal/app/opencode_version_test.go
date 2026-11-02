package app

import (
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/server"
)

func TestOpenCodeVersionIssue(t *testing.T) {
	tests := []struct {
		name  string
		major int
		ok    bool
		want  bool
	}{
		{"v1 refused", 1, true, true},
		{"v2 accepted", 2, true, false},
		{"later accepted", 3, true, false},
		{"unknown left to doctor", 0, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issue := openCodeVersionIssue(tt.major, tt.ok)
			if (issue != nil) != tt.want {
				t.Fatalf("issue = %+v, want refused=%v", issue, tt.want)
			}
			if issue != nil && (issue.ID != server.IssueOpenCodeUnsupported || !strings.Contains(issue.Message, "v1") || issue.Hint == "") {
				t.Fatalf("unclear issue: %+v", issue)
			}
		})
	}
}

func TestRequireOpenCodeV2(t *testing.T) {
	pin := func(major int, ok bool) {
		prev := installedOpenCodeMajor
		installedOpenCodeMajor = func() (int, bool) { return major, ok }
		t.Cleanup(func() { installedOpenCodeMajor = prev })
	}

	t.Run("v1 disables OpenCode only", func(t *testing.T) {
		pin(1, true)
		enabled := map[string]bool{"opencode": true, "claudecode": true}
		issue := requireOpenCodeV2(enabled)
		if issue == nil || issue.ID != server.IssueOpenCodeUnsupported {
			t.Fatalf("issue = %+v", issue)
		}
		if enabled["opencode"] || !enabled["claudecode"] {
			t.Fatalf("enabled = %v", enabled)
		}
	})

	t.Run("v2 keeps OpenCode", func(t *testing.T) {
		pin(2, true)
		enabled := map[string]bool{"opencode": true}
		if issue := requireOpenCodeV2(enabled); issue != nil || !enabled["opencode"] {
			t.Fatalf("issue = %+v, enabled = %v", issue, enabled)
		}
	})

	t.Run("disabled OpenCode is not checked", func(t *testing.T) {
		prev := installedOpenCodeMajor
		installedOpenCodeMajor = func() (int, bool) { t.Fatal("version read without OpenCode"); return 0, false }
		t.Cleanup(func() { installedOpenCodeMajor = prev })
		if issue := requireOpenCodeV2(map[string]bool{"claudecode": true}); issue != nil {
			t.Fatalf("issue = %+v", issue)
		}
	})
}
