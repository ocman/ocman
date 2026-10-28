package permissions

import (
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
)

// Claude approvals recorded before exact patterns hold raw tool arguments.
// After the startup upgrade a child inherits them as exact approvals, never
// as globs; OpenCode approvals keep OpenCode's patterns; the upgrade runs once.
func TestLegacyClaudeApprovalsInheritExact(t *testing.T) {
	st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	record := func(platform, id string, patterns ...string) {
		if err := st.RecordApprovedPermission(t.Context(), platform, "parent", state.ApprovedPermission{PermissionID: id,
			PermissionText: "bash", Patterns: patterns, ApprovedBy: state.ApprovalActorUser, Reply: state.ApprovalReplyAlways}); err != nil {
			t.Fatal(err)
		}
	}
	for _, platform := range []string{"claudecode", "r-abc:claudecode", "opencode"} {
		record(platform, "p1", "echo *")
	}
	for range 2 {
		if err := st.EscapeLegacyApprovalPatterns(t.Context(), providerprotocol.ExactPattern); err != nil {
			t.Fatal(err)
		}
	}
	for platform, want := range map[string]string{"claudecode": `echo \*`, "r-abc:claudecode": `echo \*`, "opencode": "echo *"} {
		rules, _, err := BuildInheritedRules(t.Context(), st, platform, "parent")
		if err != nil || len(rules) != 1 || rules[0] != (platforms.PermissionRule{Permission: "bash", Pattern: want, Action: "allow"}) {
			t.Fatalf("%s inherited %+v %v, want %q", platform, rules, err, want)
		}
	}
	// Rows recorded after the upgrade are already exact and stay as recorded.
	record("claudecode", "p2", providerprotocol.ExactPattern("ls ?"))
	if err := st.EscapeLegacyApprovalPatterns(t.Context(), providerprotocol.ExactPattern); err != nil {
		t.Fatal(err)
	}
	rules, _, _ := BuildInheritedRules(t.Context(), st, "claudecode", "parent")
	if len(rules) != 2 || rules[1].Pattern != `ls \?` {
		t.Fatalf("a current row was rewritten: %+v", rules)
	}
}
