package autoapprove

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/state"
	"github.com/ocman/ocman/internal/state/statetest"
)

type policyOutcomePlatform struct{ *fakePlatform }

func (policyOutcomePlatform) ReplyOutcomes(context.Context, string) bool { return false }

func TestManualRulesRequireConfirmedReplies(t *testing.T) {
	store := statetest.Open(t)
	saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "*", Pattern: "*", Action: "allow"}})
	rec := &replyRecorder{}
	adapter := policyOutcomePlatform{&fakePlatform{respondPermissionFn: rec.record}}
	svc := NewService(Deps{Store: store})
	svc.applyManualRules(t.Context(), adapter, "s", "p", askedPermission{permission: "bash"})
	if rec.count() != 0 {
		t.Fatal("unconfirmed provider was approved automatically")
	}
}

func TestManualRulesAncestryErrorsFailClosed(t *testing.T) {
	store := statetest.Open(t)
	for _, parent := range []func(context.Context, platforms.Platform, string) (string, error){
		func(context.Context, platforms.Platform, string) (string, error) {
			return "", errors.New("unreachable")
		},
		func(_ context.Context, _ platforms.Platform, id string) (string, error) { return id, nil },
		func(_ context.Context, _ platforms.Platform, id string) (string, error) { return id + "x", nil },
	} {
		svc := NewService(Deps{Store: store, PermissionParent: parent})
		if _, err := svc.manualDecision(t.Context(), &fakePlatform{}, "s", askedPermission{permission: "bash"}); err == nil {
			t.Fatal("invalid ancestry was accepted")
		}
	}
	if err := validateManualRules(make([]platforms.PermissionRule, 201)); err == nil {
		t.Fatal("unbounded rules")
	}
	if err := validateManualRules([]platforms.PermissionRule{{Permission: "bash", Pattern: strings.Repeat("x", 4097), Action: "allow"}}); err == nil {
		t.Fatal("unbounded pattern")
	}
}

func TestManualPolicyHistoryIncludesOnlyNestedDescendants(t *testing.T) {
	store := statetest.Open(t)
	for _, id := range []string{"root", "child", "grandchild", "unrelated"} {
		if err := store.EnsurePermissionInboxItem(t.Context(), state.InboxPermission{Platform: "fake", SessionID: id, PermissionID: id, Permission: "bash", Patterns: []string{providerprotocol.ExactPattern("cat *")}}); err != nil {
			t.Fatal(err)
		}
	}
	adapter := manualPolicyPlatform{fakePlatform: &fakePlatform{exactPatterns: true, detail: &platforms.SessionDetail{Session: &db.Session{ID: "root"}, SessionTree: []db.Session{
		{ID: "grandchild", ParentID: "child"}, {ID: "child", ParentID: "root"}, {ID: "unrelated"},
	}}}}
	m := newTestModule(store)
	policy, err := m.SessionPermissionPolicy(t.Context(), adapter, "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Requests) != 3 {
		t.Fatalf("history = %+v", policy.Requests)
	}
	for _, request := range policy.Requests {
		if request.SessionID == "unrelated" || request.Patterns[0] != "cat *" {
			t.Fatalf("history entry = %+v", request)
		}
	}
}

func TestManualPolicySaveAppliesToPendingAndClearsRules(t *testing.T) {
	store := statetest.Open(t)
	rec := &replyRecorder{}
	adapter := manualPolicyPlatform{fakePlatform: &fakePlatform{detail: &platforms.SessionDetail{Session: &db.Session{ID: "s"}}, respondPermissionFn: rec.record}, pending: []platforms.LivePrompt{{"id": "p", "permission": "bash", "patterns": []string{"tea do something"}}}}
	m := newTestModule(store)
	m.Session = func(http.ResponseWriter, *http.Request, string) platforms.Platform { return adapter }
	resp := serve(m, "PUT", "/api/session/s/permission-policy", `{"rules":[{"permission":"bash","pattern":"tea do *","action":"allow"}]}`)
	if resp.Code != http.StatusOK || rec.count() != 1 {
		t.Fatalf("pending rules: %d %s, replies %+v", resp.Code, resp.Body, rec.replies)
	}
}
