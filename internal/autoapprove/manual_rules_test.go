package autoapprove

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
)

func saveManualRules(t *testing.T, store *state.DB, platform, sid string, rules []platforms.PermissionRule) {
	t.Helper()
	value, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(t.Context(), manualRulesKey(platform, sid), string(value)); err != nil {
		t.Fatal(err)
	}
}

func TestManualRulesOverrideNativeRulesAndJudge(t *testing.T) {
	for _, action := range []string{"allow", "ask", "deny"} {
		t.Run(action, func(t *testing.T) {
			store := statetest.Open(t)
			if err := store.EnsurePermissionInboxItem(t.Context(), state.InboxPermission{Platform: "fake", SessionID: "child", PermissionID: "p1", Permission: "bash", Patterns: []string{"tea do something"}}); err != nil {
				t.Fatal(err)
			}
			saveManualRules(t, store, "fake", "root", []platforms.PermissionRule{{Permission: "bash", Pattern: "tea do *", Action: action}})
			rec := &replyRecorder{}
			adapter := &fakePlatform{rules: map[string][]platforms.PermissionRule{"child": yoloRules}, respondPermissionFn: rec.record}
			svc := NewService(Deps{Store: store, DefaultEnabled: true, ParentSessionID: func(_ context.Context, id string) (string, bool) { return "root", id == "child" }})
			svc.backgroundAutoApprove(t.Context(), adapter.ID(), adapter, "child", "p1", askedPermission{permission: "bash", patterns: []string{"tea do something"}})
			want := "once"
			if action == "deny" {
				want = "reject"
			}
			if action == "ask" {
				if rec.count() != 0 {
					t.Fatal("ask replied automatically")
				}
			} else if rec.count() != 1 || rec.replies[0].Reply != want {
				t.Fatalf("replies = %+v", rec.replies)
			}
			approvals, err := store.ListApprovedPermissions(t.Context(), "fake", "child")
			if err != nil || len(approvals) != 0 {
				t.Fatalf("manual rules leaked into inheritable approvals: %+v, %v", approvals, err)
			}
			history, err := store.SessionPermissionHistory(t.Context(), "fake", []string{"child"})
			if err != nil || len(history) != 1 {
				t.Fatalf("history = %+v, %v", history, err)
			}
			// A replay of an answered request must not answer twice.
			svc.applyManualRules(t.Context(), adapter, "child", "p1", askedPermission{permission: "bash", patterns: []string{"tea do something"}})
			if rec.count() > 1 {
				t.Fatal("duplicate manual response")
			}
		})
	}
}

func TestManualRulesScopeAndExactValues(t *testing.T) {
	store := statetest.Open(t)
	saveManualRules(t, store, "fake", "root", []platforms.PermissionRule{{Permission: "bash", Pattern: `cat \*`, Action: "deny"}})
	svc := NewService(Deps{Store: store, ParentSessionID: func(_ context.Context, id string) (string, bool) { return "root", id == "child" }})
	for _, tc := range []struct{ platform, sid, command, want string }{
		{"fake", "child", "cat *", "deny"},
		{"fake", "child", "cat secret", ""},
		{"fake", "other-session", "cat *", ""},
		{"other-platform", "child", "cat *", ""},
	} {
		adapter := &fakePlatform{id: platforms.ID(tc.platform), exactPatterns: true}
		got, err := svc.manualDecision(t.Context(), adapter, tc.sid, askedPermission{permission: "bash", patterns: []string{providerprotocol.ExactPattern(tc.command)}})
		if err != nil || got != tc.want {
			t.Fatalf("%+v: decision %q, %v", tc, got, err)
		}
	}
	// A child's more recent explicit rule overrides an ancestor's rule.
	saveManualRules(t, store, "fake", "child", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}})
	got, err := svc.manualDecision(t.Context(), &fakePlatform{}, "child", askedPermission{permission: "bash", patterns: []string{"cat *"}})
	if err != nil || got != "ask" {
		t.Fatalf("child override: %q, %v", got, err)
	}
}

func TestManualRulesFailClosedAndGuardLateJudge(t *testing.T) {
	store := statetest.Open(t)
	svc := NewService(Deps{Store: store})
	rec := &replyRecorder{}
	adapter := &fakePlatform{respondPermissionFn: rec.record}
	asked := askedPermission{permission: "bash", patterns: []string{"tea do something"}}
	for _, action := range []string{"ask", "deny", "allow"} {
		saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "bash", Pattern: "tea do *", Action: action}})
		unlock, proceed := svc.guardManualApproval(t.Context(), adapter, "s", action, asked)
		unlock()
		if proceed {
			t.Fatalf("late judge bypassed %s", action)
		}
	}
	if rec.count() != 2 {
		t.Fatalf("guard replies: %+v", rec.replies)
	}
	if err := store.SetSetting(t.Context(), manualRulesKey("fake", "s"), "broken"); err != nil {
		t.Fatal(err)
	}
	if !svc.applyManualRules(t.Context(), adapter, "s", "broken", asked) {
		t.Fatal("invalid stored policy fell through to judge")
	}
	if rec.count() != 2 {
		t.Fatal("invalid policy approved a request")
	}
}

func TestManualRuleReplyFailureStaysActionable(t *testing.T) {
	store := statetest.Open(t)
	saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}})
	for _, replyErr := range []error{errors.New("offline"), platforms.ErrReplyUnrecorded} {
		svc := NewService(Deps{Store: store})
		adapter := &fakePlatform{respondPermissionFn: func(platforms.RespondPermissionRequest) error { return replyErr }}
		if !svc.applyManualRules(t.Context(), adapter, "s", replyErr.Error(), askedPermission{permission: "bash"}) {
			t.Fatal("policy did not handle request")
		}
		status, ok := svc.lookupAutoApproveStatus("s", replyErr.Error())
		if !ok {
			t.Fatal("missing reply state")
		}
		if errors.Is(replyErr, platforms.ErrReplyUnrecorded) && !status.aiResponseSucceeded {
			t.Fatal("unrecorded successful response lost")
		}
		if !errors.Is(replyErr, platforms.ErrReplyUnrecorded) && status.aiResponseSucceeded {
			t.Fatal("failed response marked successful")
		}
	}
}

func TestManualRuleEnsureReplayDoesNotReplyTwice(t *testing.T) {
	store := statetest.Open(t)
	saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}})
	rec := &replyRecorder{}
	adapter := &fakePlatform{respondPermissionFn: rec.record}
	svc := NewService(Deps{Store: store})
	t.Cleanup(svc.Shutdown)
	for range 2 {
		svc.Ensure("fake", adapter, "s", "p", "bash", []string{"tea do something"}, nil)
		svc.jobs.Wait()
	}
	if rec.count() != 1 {
		t.Fatalf("answered the same request %d times", rec.count())
	}
}

func TestRemovingAskRuleResumesPendingRequest(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled", true: "cancelled judge releasing"}[running], func(t *testing.T) {
			store := statetest.Open(t)
			saveManualRules(t, store, "fake", "s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}})
			rec := &replyRecorder{}
			adapter := &fakePlatform{rules: map[string][]platforms.PermissionRule{"s": yoloRules}, respondPermissionFn: rec.record}
			svc := NewService(Deps{Store: store})
			t.Cleanup(svc.Shutdown)
			if running {
				_, _ = svc.claimAutoApproveWithStart(t.Context(), "s", "p", 0)
			}
			asked := askedPermission{permission: "bash", patterns: []string{"tea do something"}}
			svc.applyManualRules(t.Context(), adapter, "s", "p", asked)
			saveManualRules(t, store, "fake", "s", nil)
			svc.resumeAfterManualRule(adapter, "s", "p", asked)
			if running {
				if rec.count() != 0 {
					t.Fatal("restarted before old judge released")
				}
				svc.releaseAutoApprove("s", "p")
			}
			svc.jobs.Wait()
			if rec.count() != 1 {
				t.Fatalf("pending prompt was not resumed: %+v", rec.replies)
			}
		})
	}
}

type manualPolicyPlatform struct {
	*fakePlatform
	pending []platforms.LivePrompt
}

func (p manualPolicyPlatform) ListPermissions(context.Context, string) ([]platforms.LivePrompt, error) {
	return p.pending, nil
}

func TestManualPolicyHTTP(t *testing.T) {
	store := statetest.Open(t)
	adapter := manualPolicyPlatform{fakePlatform: &fakePlatform{detail: &platforms.SessionDetail{Session: &db.Session{ID: "s"}}}}
	m := newTestModule(store)
	m.Session = func(http.ResponseWriter, *http.Request, string) platforms.Platform { return adapter }
	for _, body := range []string{`{"rules":[{"permission":"bash","pattern":"tea do *","action":"maybe"}]}`, `{"rules":[{"permission":"","pattern":"*","action":"allow"}]}`, "bad json"} {
		if resp := serve(m, "PUT", "/api/session/s/permission-policy", body); resp.Code != http.StatusBadRequest {
			t.Fatalf("invalid policy status %d: %s", resp.Code, resp.Body)
		}
	}
	resp := serve(m, "PUT", "/api/session/s/permission-policy", `{"rules":[{"permission":"bash","pattern":"tea do *","action":"allow"}]}`)
	if resp.Code != http.StatusOK {
		t.Fatalf("save status %d: %s", resp.Code, resp.Body)
	}
	var policy PermissionPolicy
	if err := json.Unmarshal(resp.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	if len(policy.Rules) != 1 || policy.Requests == nil {
		t.Fatalf("policy = %+v", policy)
	}
	resp = serve(m, "GET", "/api/session/s/permission-policy", "")
	if resp.Code != http.StatusOK {
		t.Fatalf("get status %d", resp.Code)
	}
	if resp := serve(m, "DELETE", "/api/session/s/permission-policy", ""); resp.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method status %d", resp.Code)
	}
	if resp := serve(m, "PUT", "/api/session/s/permission-policy", `{"rules":[]}`); resp.Code != http.StatusOK {
		t.Fatal(resp.Body)
	}
	rules, err := m.svc.manualRules(t.Context(), "fake", "s")
	if err != nil || len(rules) != 0 {
		t.Fatalf("clear: %+v, %v", rules, err)
	}
}
