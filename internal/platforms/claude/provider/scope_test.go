package claudeprovider

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/ocman/ocman/internal/autoapprove"
	"github.com/ocman/ocman/internal/permissions"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/platforms/provider"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
)

// askFixture is one turn whose only tool call asks for tool with input.
func askFixture(t *testing.T, tool, input string) string {
	return writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":%q,"input":%s,"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`, tool, input))
}

// "Allow always" on a call whose command, path or URL contains * or ? grants
// exactly that call: the characters are not wildcards, not in the session,
// not after a restart (rules reloaded) and not in an inheriting child.
func TestAllowAlwaysWithWildcardCharactersIsExact(t *testing.T) {
	for _, c := range []struct{ tool, approved, other string }{
		{"Bash", `{"command":"echo *"}`, `{"command":"echo harmless; touch /tmp/unapproved"}`},
		{"Write", `{"file_path":"/tmp/a*b"}`, `{"file_path":"/tmp/a-secret-b"}`},
		{"WebFetch", `{"url":"https://example.com/?q=1"}`, `{"url":"https://example.com/xq=1"}`},
	} {
		t.Run(c.tool, func(t *testing.T) {
			st, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			svc := autoapprove.NewService(autoapprove.Deps{Store: st})
			h, got := promptHarness(t, askFixture(t, c.tool, c.approved), func(h *liveHarness, e providerprotocol.Event) {
				svc.HandleProviderEvent(t.Context(), providerPlatform{Adapter: h.adapter}, e)
			})
			parent := h.create(t)
			h.send(t, parent, "go")
			var pending []platforms.LivePrompt
			waitFor(t, "prompt", func() bool { pending, _ = h.adapter.ListPermissions(t.Context(), parent); return len(pending) == 1 })
			if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: parent,
				PermissionID: pending[0]["id"].(string), Reply: "always"}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "parent idle", func() bool { _, _, n := h.events.get(parent); return n == 1 })
			waitFor(t, "recorded approval", func() bool {
				a, _ := st.ListApprovedPermissions(t.Context(), PlatformID, parent)
				return len(a) == 1
			})
			// The identical call is allowed without asking.
			h.turn(t, parent, 2)
			if n := len(got.props("permission.asked")); n != 1 {
				t.Fatalf("the approved call was asked again (%d asks)", n)
			}
			// A different call that the literal characters would glob-match is asked.
			t.Setenv("FAKE_CLAUDE_FIXTURE", askFixture(t, c.tool, c.other))
			reloaded, err := provider.OpenRules(filepath.Join(h.dir, "rules.json"))
			if err != nil {
				t.Fatal(err)
			}
			h.adapter.SetRules(reloaded)
			waitFor(t, "parent child stopped", func() bool { return h.procs() == 0 })
			if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: parent, Message: "go"}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "different call asked", func() bool { return len(got.props("permission.asked")) == 2 })

			// A worktree child inherits the approval with the same scope.
			rules, _, err := permissions.BuildInheritedRules(t.Context(), st, PlatformID, parent)
			if err != nil || len(rules) != 1 {
				t.Fatalf("inherited = %+v %v", rules, err)
			}
			child := h.create(t)
			if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: child, Rules: rules}); err != nil {
				t.Fatal(err)
			}
			if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: child, Message: "go"}); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "child asked for the different call", func() bool {
				for _, p := range got.props("permission.asked") {
					if p["sessionID"] == child {
						return true
					}
				}
				return false
			})
			t.Setenv("FAKE_CLAUDE_FIXTURE", askFixture(t, c.tool, c.approved))
			other := h.create(t)
			if err := h.adapter.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: other, Rules: rules}); err != nil {
				t.Fatal(err)
			}
			h.turn(t, other, 1)
			for _, p := range got.props("permission.asked") {
				if p["sessionID"] == other {
					t.Fatalf("the inherited exact approval did not allow the approved call")
				}
			}
		})
	}
}
