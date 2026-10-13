package autoapprove

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state"
)

// listingPlatform adds a pending-permission list to the fake.
type listingPlatform struct {
	fakePlatform
	prompts []platforms.LivePrompt
	err     error
	lists   int
}

func (p *listingPlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	p.lists++
	return p.prompts, p.err
}

func TestPromptFields(t *testing.T) {
	entry := platforms.LivePrompt{"sessionID": "child", "patterns": []any{"git status", 3}, "metadata": map[string]any{"k": "v"}}
	if got := PromptSessionID(entry, "parent"); got != "child" {
		t.Fatalf("session = %q, want the issuing child", got)
	}
	if got := PromptSessionID(platforms.LivePrompt{}, "parent"); got != "parent" {
		t.Fatalf("fallback session = %q", got)
	}
	if got := PromptPatterns(entry); len(got) != 1 || got[0] != "git status" {
		t.Fatalf("patterns = %v", got)
	}
	if PromptPatterns(platforms.LivePrompt{}) != nil || PromptMetadata(platforms.LivePrompt{"metadata": "x"}) != nil {
		t.Fatal("absent or malformed fields must read as nil")
	}
	if PromptMetadata(entry)["k"] != "v" {
		t.Fatalf("metadata = %v", PromptMetadata(entry))
	}
}

// Rules apply to a subagent's pending prompt under its own session id, skip
// incomplete entries, and never run on the hub for a remote's prompts.
func TestApplyRulesToPending(t *testing.T) {
	rec := &replyRecorder{}
	local := &listingPlatform{
		fakePlatform: fakePlatform{id: "opencode", rules: map[string][]platforms.PermissionRule{"child": yoloRules}, respondPermissionFn: rec.record},
		prompts: []platforms.LivePrompt{
			{"id": "perm", "permission": "bash", "sessionID": "child", "patterns": []any{"ls"}},
			{"id": "", "permission": "bash"},
			{"id": "no-permission"},
		},
	}
	m := NewModule(ModuleDeps{})
	m.ApplyRulesToPending(t.Context(), local, "parent")
	if rec.count() != 1 || rec.replies[0].SessionID != "child" || rec.replies[0].PermissionID != "perm" {
		t.Fatalf("replies = %#v", rec.replies)
	}

	remote := &listingPlatform{fakePlatform: fakePlatform{id: "r-host:opencode", rules: local.rules, respondPermissionFn: rec.record}, prompts: local.prompts}
	m.ApplyRulesToPending(t.Context(), remote, "parent")
	if remote.lists != 0 || rec.count() != 1 {
		t.Fatalf("hub acted on a remote's prompts: %d lists, %d replies", remote.lists, rec.count())
	}
	failing := &listingPlatform{fakePlatform: fakePlatform{id: "opencode"}, err: errors.New("down")}
	m.ApplyRulesToPending(t.Context(), failing, "parent") // logged, not fatal
}

// EnsurePending records each prompt for the Inbox under the listed session,
// whatever the judge then decides.
func TestEnsurePendingObservesPrompts(t *testing.T) {
	var asked []state.InboxPermission
	m := NewModule(ModuleDeps{Engine: Deps{PermissionAsked: func(p state.InboxPermission) { asked = append(asked, p) }}})
	m.EnsurePending(&fakePlatform{id: "opencode"}, "ses", []platforms.LivePrompt{
		{"id": "perm", "permission": "bash", "patterns": []any{"ls"}},
		{"id": "skipped"},
	})
	if len(asked) != 1 || asked[0].SessionID != "ses" || asked[0].PermissionID != "perm" || asked[0].Patterns[0] != "ls" {
		t.Fatalf("asked = %#v", asked)
	}
}

// Enabling auto-approve on a remote session leaves judging to its owner.
func TestResumePendingSkipsRemote(t *testing.T) {
	remote := &listingPlatform{fakePlatform: fakePlatform{id: "r-host:opencode"}}
	NewModule(ModuleDeps{}).resumePending(t.Context(), remote, "ses")
	if remote.lists != 0 {
		t.Fatal("hub listed a remote's permissions")
	}
	failing := &listingPlatform{fakePlatform: fakePlatform{id: "opencode"}, err: errors.New("down")}
	NewModule(ModuleDeps{}).resumePending(t.Context(), failing, "ses")
	if failing.lists != 1 {
		t.Fatal("local resume did not list permissions")
	}
}
