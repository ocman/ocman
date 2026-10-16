package autoapprove

import (
	"context"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

var yoloRules = []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}}

type replyRecorder struct {
	mu      sync.Mutex
	replies []platforms.RespondPermissionRequest
}

func (r *replyRecorder) record(req platforms.RespondPermissionRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replies = append(r.replies, req)
	return nil
}

func (r *replyRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.replies)
}

func TestApplySessionRules(t *testing.T) {
	parents := map[string]string{"child": "parent"}
	tests := []struct {
		name      string
		sessionID string
		rules     map[string][]platforms.PermissionRule
		want      bool
	}{
		{"yolo session", "parent", map[string][]platforms.PermissionRule{"parent": yoloRules}, true},
		{"default session", "parent", map[string][]platforms.PermissionRule{}, false},
		{"subagent of yolo session", "child", map[string][]platforms.PermissionRule{"parent": yoloRules}, true},
		{"subagent deny still wins", "child", map[string][]platforms.PermissionRule{
			"parent": yoloRules,
			"child":  {{Permission: "bash", Pattern: "*", Action: "deny"}},
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &replyRecorder{}
			fp := &fakePlatform{id: "opencode", rules: tt.rules, respondPermissionFn: rec.record}
			svc := NewService(Deps{ParentSessionID: func(_ context.Context, id string) (string, bool) {
				p, ok := parents[id]
				return p, ok
			}})
			got := svc.ApplySessionRules(t.Context(), "opencode", fp, tt.sessionID, "perm", "bash", []string{"rm -rf build"}, nil)
			if got != tt.want || rec.count() != map[bool]int{true: 1, false: 0}[tt.want] {
				t.Fatalf("ApplySessionRules = %v with %d replies, want %v", got, rec.count(), tt.want)
			}
			if !tt.want {
				return
			}
			if req := rec.replies[0]; req.Reply != "once" || req.SessionID != tt.sessionID || req.PermissionID != "perm" {
				t.Fatalf("reply = %#v", req)
			}
			// A second sweep, or the asked-event path, must not answer twice.
			if svc.ApplySessionRules(t.Context(), "opencode", fp, tt.sessionID, "perm", "bash", []string{"rm -rf build"}, nil) || rec.count() != 1 {
				t.Fatalf("prompt answered twice: %d replies", rec.count())
			}
		})
	}
}

// TestApplySessionRulesExactPatterns covers provider (Claude) grants, which
// are stored escaped: an "Allow always" for `cat *` is `cat \*` and must not
// read as OpenCode's `cat /*`, while OpenCode rules keep their own semantics.
func TestApplySessionRulesExactPatterns(t *testing.T) {
	exact := providerprotocol.ExactPattern
	parents := map[string]string{"child": "parent"}
	tests := []struct {
		name    string
		exact   bool
		rule    string
		pattern string
		want    bool
	}{
		{"exact star grant does not glob", true, exact("cat *"), exact("cat /etc/shadow"), false},
		{"exact question grant does not glob", true, exact("rm foo?"), exact("rm foo/x"), false},
		{"exact star grant allows its own call", true, exact("cat *"), exact("cat *"), true},
		{"provider glob rule still globs", true, "git *", exact("git status"), true},
		{"provider escaped backslash is literal", true, exact(`printf '\*'`), exact(`printf '\*'`), true},
		{"opencode backslash still reads as slash", false, `cat \*`, "cat /etc/shadow", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &replyRecorder{}
			rules := map[string][]platforms.PermissionRule{"parent": {{Permission: "bash", Pattern: tt.rule, Action: "allow"}}}
			fp := &fakePlatform{id: "claudecode", rules: rules, exactPatterns: tt.exact, respondPermissionFn: rec.record}
			svc := NewService(Deps{ParentSessionID: func(_ context.Context, id string) (string, bool) {
				p, ok := parents[id]
				return p, ok
			}})
			if got := svc.ApplySessionRules(t.Context(), "claudecode", fp, "child", "perm", "bash", []string{tt.pattern}, nil); got != tt.want {
				t.Fatalf("ApplySessionRules(rule %q, pattern %q) = %v, want %v", tt.rule, tt.pattern, got, tt.want)
			}
		})
	}
}

func TestApplySessionRulesCancelsRunningJudge(t *testing.T) {
	rec := &replyRecorder{}
	fp := &fakePlatform{id: "opencode", rules: map[string][]platforms.PermissionRule{"s": yoloRules}, respondPermissionFn: rec.record}
	svc := NewService(Deps{})
	ctx, ok := svc.claimAutoApprove(context.Background(), "s", "perm")
	if !ok {
		t.Fatal("claim failed")
	}
	if !svc.ApplySessionRules(t.Context(), "opencode", fp, "s", "perm", "bash", []string{"ls"}, nil) {
		t.Fatal("not approved")
	}
	if ctx.Err() == nil {
		t.Fatal("running judge was not cancelled")
	}
}

// TestBackgroundAutoApproveHonoursSessionRules covers a prompt raised by a turn
// that started before the switch to YOLO: it is answered even though the judge
// is off, instead of being left for the user.
func TestBackgroundAutoApproveHonoursSessionRules(t *testing.T) {
	rec := &replyRecorder{}
	needsUser := 0
	fp := &fakePlatform{id: "opencode", rules: map[string][]platforms.PermissionRule{"s": yoloRules}, respondPermissionFn: rec.record}
	svc := NewService(Deps{PromptNeedsUser: func(string, string, string, string) { needsUser++ }})
	svc.backgroundAutoApprove(t.Context(), "opencode", fp, "s", "perm", askedPermission{platformID: "opencode", permission: "bash", patterns: []string{"ls"}})
	if rec.count() != 1 || needsUser != 0 {
		t.Fatalf("replies = %d, needsUser = %d; want 1, 0", rec.count(), needsUser)
	}
}
