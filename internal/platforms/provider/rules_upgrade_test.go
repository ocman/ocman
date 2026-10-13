package provider

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/NoUseFreak/ocman/internal/permissions"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
)

type rulesReader struct{ r *Rules }

func (l rulesReader) PermissionRules(_, id string) ([]platforms.PermissionRule, error) {
	return l.r.get(id), nil
}

// A child forked before exact patterns holds its parent's literal approvals
// as globs in its ruleset. After the upgrade neither the child nor a
// grandchild forked from it afterwards may allow more than the approved
// call; user rules keep their globs and denies stay.
func TestLegacyInheritedRulesBecomeExact(t *testing.T) {
	dir := t.TempDir()
	st, err := state.Open(filepath.Join(dir, "state.db"))
	requireNoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	for id, p := range map[string][2]string{"p1": {"bash", "echo *"}, "p2": {"webfetch", "https://example.com/?q=1"}} {
		requireNoError(t, st.RecordApprovedPermission(t.Context(), "claudecode", "parent", state.ApprovedPermission{PermissionID: id,
			PermissionText: p[0], Patterns: []string{p[1]}, ApprovedBy: state.ApprovalActorUser, Reply: state.ApprovalReplyAlways}))
	}
	// The pre-upgrade store: the parent's raw approval and the child's
	// inherited copy (webfetch inherited flat, as before exact scopes),
	// next to rules the user wrote.
	user := []platforms.PermissionRule{
		{Permission: "bash", Pattern: "git *", Action: "allow"},
		{Permission: "bash", Pattern: "echo *", Action: "deny"},
	}
	legacy := map[string]any{
		"approved": map[string]any{"parent": []platforms.PermissionRule{{Permission: "bash", Pattern: "echo *", Action: "allow"}}},
		"rules": map[string]any{"child": []platforms.PermissionRule{
			{Permission: "bash", Pattern: "echo *", Action: "allow"},
			{Permission: "webfetch", Pattern: "", Action: "allow"},
		}, "custom": user},
	}
	data, _ := json.Marshal(legacy)
	path := filepath.Join(dir, "rules.json")
	requireNoError(t, os.WriteFile(path, data, 0o600))

	upgrade := func() *Rules { // claude_platform.go's startup order
		requireNoError(t, st.EscapeLegacyApprovalPatterns(t.Context(), providerprotocol.ExactPattern))
		r := mustRules(t, path)
		approvals, err := st.ListPlatformApprovals(t.Context(), "claudecode")
		requireNoError(t, err)
		r.EscapeInheritedApprovals(approvals)
		return r
	}
	r := upgrade()
	inherited, _, err := permissions.BuildInheritedRulesWithLive(context.Background(), st, rulesReader{r}, "claudecode", "child")
	requireNoError(t, err)
	requireNoError(t, r.set("grandchild", inherited))
	r = upgrade() // a second start converts nothing again
	prompt := func(session, permission, value string) platforms.LivePrompt {
		return platforms.LivePrompt{"sessionID": session, "permission": permission, "patterns": []any{providerprotocol.ExactPattern(value)}}
	}
	for _, session := range []string{"child", "grandchild"} {
		for value, want := range map[string]string{
			"echo *":                               "once",
			"echo harmless; touch /tmp/unapproved": "",
		} {
			if got := r.Decide(prompt(session, "bash", value)); got != want {
				t.Errorf("%s %q = %q, want %q", session, value, got, want)
			}
		}
		if got := r.Decide(prompt(session, "webfetch", "https://evil.example/")); got != "" {
			t.Errorf("%s fetches any URL: %q", session, got)
		}
	}
	if got := r.Decide(prompt("custom", "bash", "git status")); got != "once" {
		t.Errorf("user glob lost: %q", got)
	}
	if got := r.Decide(prompt("custom", "bash", "echo hi")); got != "reject" {
		t.Errorf("user deny lost: %q", got)
	}
}
