package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

func TestPreflightRulesContinueWithoutGrantingNativePermission(t *testing.T) {
	for _, tc := range []struct{ action, want string }{
		{"", "once"}, {"allow", "once"}, {"deny", "reject"}, {"ask", ""},
	} {
		t.Run(tc.action, func(t *testing.T) {
			r, err := OpenRules("")
			if err != nil {
				t.Fatal(err)
			}
			if tc.action != "" {
				requireNoError(t, r.set("s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: tc.action}}))
			}
			prompt := asked("p", "bash", "git status")
			prompt["metadata"] = map[string]any{"ocmanPreflight": true}
			if got := r.Decide(prompt); got != tc.want {
				t.Fatalf("decision=%q want=%q", got, tc.want)
			}
			delete(prompt, "metadata")
			if tc.action == "" && r.Decide(prompt) != "" {
				t.Fatal("normal prompts must still ask")
			}
		})
	}
}

func TestPreflightWithoutRulesStoreFailsClosed(t *testing.T) {
	a := &Adapter{}
	_, reply := a.ruleDecision(providerprotocol.Event{Type: "permission.asked", Properties: []byte(`{"id":"p","sessionID":"s","metadata":{"ocmanPreflight":true}}`)})
	if reply != "reject" {
		t.Fatalf("reply=%q", reply)
	}
}

func TestNativePermissionCapabilityFailsClosed(t *testing.T) {
	a := &Adapter{}
	if _, err := a.NativePermissionState(context.Background(), "s"); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("read=%v", err)
	}
	auto := "auto"
	if err := a.SetPermissionRules(context.Background(), platforms.SetPermissionRulesRequest{SessionID: "s", NativeMode: &auto}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatalf("write=%v", err)
	}
}

func TestSavedApprovalCannotBypassExplicitDeny(t *testing.T) {
	r, err := OpenRules("")
	if err != nil {
		t.Fatal(err)
	}
	rule := platforms.PermissionRule{Permission: "bash", Pattern: "git push*", Action: "allow"}
	requireNoError(t, r.approve("s", []platforms.PermissionRule{rule}))
	rule.Action = "deny"
	requireNoError(t, r.set("s", []platforms.PermissionRule{rule}))
	for _, preflight := range []bool{false, true} {
		prompt := asked("p", "bash", "git push origin")
		prompt["metadata"] = map[string]any{"ocmanPreflight": preflight}
		if got := r.Decide(prompt); got != "reject" {
			t.Fatalf("preflight=%v decision=%q; saved approval bypassed deny", preflight, got)
		}
	}
}
