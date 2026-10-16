package permissions

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

type fuzzApprovals []state.ApprovedPermission

func (l fuzzApprovals) ListApprovedPermissions(context.Context, string, string) ([]state.ApprovedPermission, error) {
	return l, nil
}

type fuzzLive []platforms.PermissionRule

func (l fuzzLive) PermissionRules(string, string) ([]platforms.PermissionRule, error) { return l, nil }

// FuzzInheritedLiveRulesWin checks that BuildInheritedRulesWithLive keeps the
// parent's live ruleset intact and last, however many approvals compete for
// the cap: the newest approvals fill what is left of the budget, and any
// request the live rules decide is decided the same way for the child
// (last match wins), so a live deny is never undone by an inherited allow.
func FuzzInheritedLiveRulesWin(f *testing.F) {
	f.Add(uint16(500), "edit", "*", "edit", "src/a.go")
	f.Add(uint16(3), "bash", "rm *", "bash", "rm -rf /")
	f.Add(uint16(0), "bash", "*", "bash", "ls")
	f.Add(uint16(1100), "*", "*", "read", "/etc/passwd")
	f.Fuzz(func(t *testing.T, n uint16, livePerm, livePattern, askPerm, askPattern string) {
		var approvals fuzzApprovals
		for i := range int(n % 1200) {
			approvals = append(approvals, state.ApprovedPermission{
				PermissionText: askPerm,
				Patterns:       []string{fmt.Sprintf("p%d", i), askPattern},
				ApprovedBy:     state.ApprovalActorAI,
			})
		}
		live := fuzzLive{
			{Permission: livePerm, Pattern: livePattern, Action: "deny"},
			{Permission: livePerm, Pattern: "allowed/" + livePattern, Action: "allow"},
		}
		ctx := context.Background()
		approved, _, err := BuildInheritedRules(ctx, approvals, "opencode", "parent")
		if err != nil {
			t.Fatal(err)
		}
		merged, count, err := BuildInheritedRulesWithLive(ctx, approvals, live, "opencode", "parent")
		if err != nil {
			t.Fatal(err)
		}
		if count != len(merged) {
			t.Fatalf("count %d, %d rules", count, len(merged))
		}
		if len(merged) < len(live) || !slices.Equal(merged[len(merged)-len(live):], live) {
			t.Fatalf("live rules not kept last: %v", merged)
		}
		kept := merged[:len(merged)-len(live)]
		budget := max(maxInheritedPatterns-len(live), 0)
		if want := approved[len(approved)-min(len(approved), budget):]; !slices.Equal(kept, want) {
			t.Fatalf("kept %d approvals, want the newest %d of %d", len(kept), len(want), len(approved))
		}
		for _, pattern := range []string{askPattern, "allowed/" + askPattern} {
			if evaluate(live, askPerm, pattern, wildcardMatch) == "ask" {
				continue
			}
			if got, want := Allows(merged, askPerm, []string{pattern}), Allows(live, askPerm, []string{pattern}); got != want {
				t.Fatalf("%s %q: child allows=%v, parent live allows=%v", askPerm, pattern, got, want)
			}
		}
	})
}
