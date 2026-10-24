package autoapprove

import (
	"context"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

type scopedManualPlatform struct {
	manualPolicyPlatform
	prompts map[string][]platforms.LivePrompt
}

func (p scopedManualPlatform) ListPermissions(_ context.Context, id string) ([]platforms.LivePrompt, error) {
	return p.prompts[id], nil
}

func TestParentPolicyUpdateSweepsSessionFilteredDescendants(t *testing.T) {
	for _, action := range []string{"allow", "clear"} {
		t.Run(action, func(t *testing.T) {
			store := statetest.Open(t)
			ask := []platforms.PermissionRule{{Permission: "bash", Pattern: "tea do *", Action: "ask"}}
			saveManualRules(t, store, "fake", "root", ask)
			rec := &replyRecorder{}
			adapter := scopedManualPlatform{manualPolicyPlatform: manualPolicyPlatform{fakePlatform: &fakePlatform{
				detail: &platforms.SessionDetail{Session: &db.Session{ID: "root"}, SessionTree: []db.Session{{ID: "child", ParentID: "root"}}},
				rules:  map[string][]platforms.PermissionRule{"child": yoloRules}, respondPermissionFn: rec.record,
			}}, prompts: map[string][]platforms.LivePrompt{"child": {{"id": "p", "sessionID": "child", "permission": "bash", "patterns": []string{"tea do something"}}}}}
			m := newTestModule(store)
			m.svc.deps.ParentSessionID = func(_ context.Context, id string) (string, bool) { return "root", id == "child" }
			t.Cleanup(m.svc.Shutdown)
			m.svc.applyManualRules(t.Context(), adapter, "child", "p", askedPermission{permission: "bash", patterns: []string{"tea do something"}})
			replacement := []platforms.PermissionRule{}
			if action == "allow" {
				replacement = []platforms.PermissionRule{{Permission: "bash", Pattern: "tea do *", Action: "allow"}}
			}
			if _, err := m.SessionPermissionPolicy(t.Context(), adapter, "root", &replacement); err != nil {
				t.Fatal(err)
			}
			m.svc.jobs.Wait()
			if rec.count() != 1 || rec.replies[0].SessionID != "child" {
				t.Fatalf("pending descendant was not reconsidered: %+v", rec.replies)
			}
		})
	}
}

func TestManualDenyReplyBeforeReturnKeepsRuleAttribution(t *testing.T) {
	store := statetest.Open(t)
	saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}})
	resolved := false
	adapter := &fakePlatform{}
	// The resolution is broadcast under the session's own platform.
	svc := NewService(Deps{Store: store, BroadcastPermissionResolved: func(platform, _, _, reason string) {
		resolved = platform == string(adapter.ID()) && reason == "session-rule-deny"
	}})
	adapter.respondPermissionFn = func(req platforms.RespondPermissionRequest) error {
		svc.HandlePermissionReplied(t.Context(), req.SessionID, req.PermissionID, req.Reply)
		return nil
	}
	asked := svc.rememberAsked("fake", "s", "p", "bash", []string{"tea do something"}, nil)
	svc.applyManualRules(t.Context(), adapter, "s", "p", asked)
	st, ok := svc.lookupAutoApproveStatus("s", "p")
	if !ok || !st.aiResponseSucceeded || st.manualResolvedAt != 0 || !resolved {
		t.Fatalf("automatic rejection was attributed to the user: %+v, broadcast=%v", st, resolved)
	}
}
