package opencode

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/NoUseFreak/ocman/internal/ocapi"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestPermissionRulesOnPort_DecodesRuleset verifies the adapter reads
// the session's `permission` array from GET /session/{id}.
func TestPermissionRulesOnPort_DecodesRuleset(t *testing.T) {
	port := fakePort(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_abc" {
			t.Errorf("path = %q, want /session/ses_abc", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"ses_abc","permission":[{"permission":"edit","pattern":"*","action":"deny"}]}`))
	})
	rules, err := permissionRulesOnPort(port, "ses_abc")
	if err != nil {
		t.Fatalf("permissionRulesOnPort: %v", err)
	}
	want := []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "deny"}}
	if len(rules) != 1 || rules[0] != want[0] {
		t.Errorf("rules = %+v, want %+v", rules, want)
	}
}

// TestPermissionRulesOnPort_NullPermissionMeansEmpty ensures a session
// without a ruleset (permission: null / absent) comes back as an empty,
// non-nil slice so the wire shape is always an array.
func TestPermissionRulesOnPort_NullPermissionMeansEmpty(t *testing.T) {
	port := fakePort(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"ses_abc","permission":null}`))
	})
	rules, err := permissionRulesOnPort(port, "ses_abc")
	if err != nil {
		t.Fatalf("permissionRulesOnPort: %v", err)
	}
	if rules == nil || len(rules) != 0 {
		t.Errorf("rules = %#v, want empty non-nil slice", rules)
	}
}

// TestPermissionRulesOnPort_UnreachableUpstream maps a failed fetch to
// ErrPlatformUnreachable.
func TestPermissionRulesOnPort_UnreachableUpstream(t *testing.T) {
	port := fakePort(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, err := permissionRulesOnPort(port, "ses_abc")
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Errorf("err = %v, want ErrPlatformUnreachable", err)
	}
}

// The SetPermissionRules write path (body shape, nil → [], failure
// mapping) is pinned by the mutatingOps tables in
// operations_more_test.go.

// TestAdapterPermissionRules_EndToEnd drives the exported read method
// through resolvePort against the OpenCode fake — covering the
// port-resolution wrapper, not just the *OnPort helper.
func TestAdapterPermissionRules_EndToEnd(t *testing.T) {
	const sid = "sess-perm-rules"
	const dir = "/tmp/proj-perm-rules"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"`+sid+`","directory":"`+dir+`","permission":[{"permission":"edit","pattern":"*","action":"deny"}]}`))
	withTestPort(t, dir, fake.Port())
	a := New(newTestDBWithSession(t, sid, dir), nil, nil, ocapi.Auth{})

	rules, err := a.PermissionRules(context.Background(), sid)
	if err != nil {
		t.Fatalf("PermissionRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Action != "deny" {
		t.Errorf("rules = %+v, want one deny rule", rules)
	}
}

// TestAdapterPermissionRules_UnknownSession maps an unknown session to
// an error from port resolution.
func TestAdapterPermissionRules_UnknownSession(t *testing.T) {
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	restoreServers := setDiscoverServersImplForTests(func() []openCodeServer { return nil })
	resetPortCacheForTests()
	resetSessionPortAffinityForTests()
	t.Cleanup(func() {
		restore()
		restoreServers()
		resetPortCacheForTests()
		resetSessionPortAffinityForTests()
	})

	a := &Adapter{rt: td}
	if _, err := a.PermissionRules(context.Background(), "nope"); err == nil {
		t.Error("PermissionRules: expected error for unknown session")
	}
}
