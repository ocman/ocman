package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestPermissionRulesOnPort_DecodesRuleset verifies the adapter reads
// the session's `permission` array from GET /session/{id}.
func TestPermissionRulesOnPort_DecodesRuleset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/session/ses_abc" {
			t.Errorf("path = %q, want /session/ses_abc", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"id":"ses_abc","permission":[{"permission":"edit","pattern":"*","action":"deny"}]}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":"ses_abc","permission":null}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	_, err := permissionRulesOnPort(port, "ses_abc")
	if !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Errorf("err = %v, want ErrPlatformUnreachable", err)
	}
}

// TestSetPermissionRulesOnPort_PatchesRuleset verifies the write path
// PATCHes /session/{id} with {"permission":[...]}.
func TestSetPermissionRulesOnPort_PatchesRuleset(t *testing.T) {
	var (
		gotMethod string
		gotPath   string
		gotBody   map[string][]platforms.PermissionRule
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := setPermissionRulesOnPort(context.Background(), port, platforms.SetPermissionRulesRequest{
		SessionID: "ses_abc",
		Rules:     []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}},
	})
	if err != nil {
		t.Fatalf("setPermissionRulesOnPort: %v", err)
	}
	if gotMethod != http.MethodPatch {
		t.Errorf("method = %q, want PATCH", gotMethod)
	}
	if gotPath != "/session/ses_abc" {
		t.Errorf("path = %q, want /session/ses_abc", gotPath)
	}
	if len(gotBody["permission"]) != 1 || gotBody["permission"][0].Permission != "bash" {
		t.Errorf("body.permission = %+v, want one bash rule", gotBody["permission"])
	}
}

// TestSetPermissionRulesOnPort_NilRulesSendsEmptyArray ensures a nil
// ruleset is sent as [] (restore defaults), never as null — OpenCode's
// schema wants an array.
func TestSetPermissionRulesOnPort_NilRulesSendsEmptyArray(t *testing.T) {
	var raw []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := setPermissionRulesOnPort(context.Background(), port, platforms.SetPermissionRulesRequest{
		SessionID: "ses_abc",
	})
	if err != nil {
		t.Fatalf("setPermissionRulesOnPort: %v", err)
	}
	if !strings.Contains(string(raw), `"permission":[]`) {
		t.Errorf("body = %s, want \"permission\":[]", raw)
	}
}

// TestAdapterPermissionRules_EndToEnd drives the exported adapter
// methods through resolvePort against the OpenCode fake — covering
// the port-resolution wrappers, not just the *OnPort helpers.
func TestAdapterPermissionRules_EndToEnd(t *testing.T) {
	const sid = "sess-perm-rules"
	const dir = "/tmp/proj-perm-rules"

	fake := newOpencodeFake(t)
	fake.SetSession(sid, []byte(`{"id":"`+sid+`","directory":"`+dir+`","permission":[{"permission":"edit","pattern":"*","action":"deny"}]}`))
	withTestPort(t, dir, fake.Port())
	a := New(newTestDBWithSession(t, sid, dir), nil)

	rules, err := a.PermissionRules(context.Background(), sid)
	if err != nil {
		t.Fatalf("PermissionRules: %v", err)
	}
	if len(rules) != 1 || rules[0].Action != "deny" {
		t.Errorf("rules = %+v, want one deny rule", rules)
	}

	if err := a.SetPermissionRules(context.Background(), platforms.SetPermissionRulesRequest{
		SessionID: sid,
		Rules:     []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}},
	}); err != nil {
		t.Fatalf("SetPermissionRules: %v", err)
	}
}

// TestAdapterPermissionRules_UnknownSession maps an unknown session to
// an error from port resolution on both methods.
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

	a := &Adapter{}
	if _, err := a.PermissionRules(context.Background(), "nope"); err == nil {
		t.Error("PermissionRules: expected error for unknown session")
	}
	if err := a.SetPermissionRules(context.Background(), platforms.SetPermissionRulesRequest{SessionID: "nope"}); err == nil {
		t.Error("SetPermissionRules: expected error for unknown session")
	}
}

// TestSetPermissionRulesOnPort_PropagatesUpstreamRejection maps a 4xx
// to ErrUpstreamRejected like the other mutations.
func TestSetPermissionRulesOnPort_PropagatesUpstreamRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"name":"BadRequest","data":{"message":"bad ruleset"}}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	err := setPermissionRulesOnPort(context.Background(), port, platforms.SetPermissionRulesRequest{
		SessionID: "ses_abc",
		Rules:     []platforms.PermissionRule{{Permission: "edit", Pattern: "*", Action: "allow"}},
	})
	if !errors.Is(err, platforms.ErrUpstreamRejected) {
		t.Errorf("err = %v, want ErrUpstreamRejected", err)
	}
}
