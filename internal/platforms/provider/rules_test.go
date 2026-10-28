package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
	"github.com/ocman/ocman/internal/testutil"
)

// approve grants rules to session id through the production "always"
// path: a tentative approval settled as always.
func (r *Rules) approve(id string, rules []platforms.PermissionRule) error {
	pid := "approve-" + id
	if err := r.approveTentatively(id, pid, rules); err != nil {
		return err
	}
	return r.settle(pid, true)
}

func asked(id, permission string, patterns ...string) platforms.LivePrompt {
	p := platforms.LivePrompt{"id": id, "sessionID": "s", "permission": permission}
	if patterns != nil {
		list := []any{}
		for _, pattern := range patterns {
			list = append(list, pattern)
		}
		p["patterns"] = list
	}
	return p
}

func TestRulesDecide(t *testing.T) {
	r, err := OpenRules(filepath.Join(t.TempDir(), "rules.json"))
	requireNoError(t, err)
	if got := r.Decide(asked("1", "bash", "ls")); got != "" {
		t.Fatalf("no rules must ask, got %q", got)
	}
	requireNoError(t, r.set("s", []platforms.PermissionRule{
		{Permission: "bash", Pattern: "git *", Action: "allow"},
		{Permission: "bash", Pattern: "git push*", Action: "deny"},
		{Permission: "edit", Pattern: "*", Action: "allow"},
		{Permission: "todowrite", Pattern: "", Action: "allow"},
	}))
	for prompt, want := range map[*platforms.LivePrompt]string{
		ptr(asked("1", "bash", "git status")):          "once",
		ptr(asked("2", "bash", "git")):                 "once", // "git *" also matches the bare command
		ptr(asked("3", "bash", "git push origin")):     "reject",
		ptr(asked("4", "bash", "git log", "rm -rf")):   "",
		ptr(asked("5", "edit", "/a/b.go")):             "once",
		ptr(asked("6", "todowrite")):                   "once",
		ptr(asked("7", "bash", "git log", "git push")): "reject",
		ptr(asked("8", "read", "/x")):                  "",
	} {
		if got := r.Decide(*prompt); got != want {
			t.Errorf("%v = %q, want %q", *prompt, got, want)
		}
	}
	// A reloaded store keeps the rules; an empty set restores asking.
	again, err := OpenRules(r.path)
	requireNoError(t, err)
	if len(again.get("s")) != 4 {
		t.Fatalf("reloaded = %+v", again.get("s"))
	}
	requireNoError(t, again.set("s", nil))
	if again.Decide(asked("1", "bash", "git status")) != "" || len(mustRules(t, r.path).get("s")) != 0 {
		t.Fatal("empty ruleset must restore the default")
	}
}

func ptr[T any](v T) *T { return &v }

// A ruleset or "Allow always" that cannot be persisted is reported, and the
// store keeps answering with what is on disk, so a restart cannot revert an
// acknowledged change.
func TestRulesPersistFailureIsVisible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.json")
	r, err := OpenRules(path)
	requireNoError(t, err)
	allow := []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}
	requireNoError(t, r.set("s", allow))
	// Make the next write fail: the temp file's name is a directory.
	requireNoError(t, os.Mkdir(path+".tmp", 0o700))
	requireNoError(t, os.WriteFile(filepath.Join(path+".tmp", "x"), nil, 0o600))
	if err := r.set("s", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}); err == nil {
		t.Fatal("a failed write must be reported")
	}
	if got := r.Decide(asked("1", "bash", "ls")); got != "once" {
		t.Fatalf("unpersisted deny took effect in memory: %q", got)
	}
	if err := r.approve("s", allow); err == nil || len(r.data.Approved["s"]) != 0 {
		t.Fatalf("approve = %v, approved = %+v", err, r.data.Approved)
	}
	again, err := OpenRules(path)
	requireNoError(t, err)
	if got := again.Decide(asked("1", "bash", "ls")); got != "once" {
		t.Fatalf("restart = %q", got)
	}
	// An unreadable store fails to open instead of starting empty.
	requireNoError(t, os.WriteFile(path, []byte("{"), 0o600))
	if _, err := OpenRules(path); err == nil {
		t.Fatal("a corrupt store must fail to open")
	}
}

// The adapter returns persistence failures to the caller.
func TestAdapterRulesPersistFailure(t *testing.T) {
	f := newFake(t, allCapabilities())
	a := f.adapter(t)
	path := filepath.Join(t.TempDir(), "rules.json")
	r, err := OpenRules(path)
	requireNoError(t, err)
	a.SetRules(r)
	requireNoError(t, os.Mkdir(path+".tmp", 0o700))
	requireNoError(t, os.WriteFile(filepath.Join(path+".tmp", "x"), nil, 0o600))
	if err := a.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s",
		Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}}); err == nil {
		t.Fatal("SetPermissionRules acknowledged an unpersisted ruleset")
	}
	f.reply("POST", "/permissions/p2/reply", nil)
	a.mu.Lock()
	a.permissions["p2"] = asked("p2", "bash", "make")
	a.mu.Unlock()
	if err := a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p2", Reply: "always"}); err == nil {
		t.Fatal("an unpersisted Allow always must be reported")
	}
}

// Rule-decided prompts are answered by the adapter and never reach the
// handler, the pending list or a browser; the rest flow through as before.
// "Allow always" is remembered for the session.
func TestAdapterAnswersByRule(t *testing.T) {
	caps := allCapabilities()
	f := newFake(t, caps)
	events := []providerprotocol.Event{
		{Type: "permission.asked", Properties: mustJSON(asked("p1", "bash", "rm -rf /"))},
		{Type: "permission.asked", Properties: mustJSON(asked("p2", "bash", "make"))},
		{Type: "permission.replied", Properties: mustJSON(map[string]any{"sessionID": "s", "requestID": "p1", "reply": "reject"})},
	}
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i, e := range events {
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i+1, mustJSON(e))
		}
	}
	a := f.adapter(t)
	a.SetRules(mustRules(t, ""))
	requireNoError(t, a.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s",
		Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "rm *", Action: "deny"}}}))
	if rules, err := a.PermissionRules(t.Context(), "s"); err != nil || len(rules) != 1 {
		t.Fatalf("rules = %+v %v", rules, err)
	}
	var handled []string
	a.SetEventHandler(func(e providerprotocol.Event) {
		var p platforms.LivePrompt
		_ = json.Unmarshal(e.Properties, &p)
		handled = append(handled, e.Type+":"+fmt.Sprint(p["id"], p["requestID"]))
	})
	var browser bytes.Buffer
	_ = a.ProxyEvents(t.Context(), "s", &browser, nil)
	if strings.Contains(browser.String(), "rm -rf") || !strings.Contains(browser.String(), "make") {
		t.Fatalf("browser saw the rule-denied prompt: %s", browser.String())
	}
	if len(handled) != 1 || handled[0] != "permission.asked:p2<nil>" {
		t.Fatalf("handled = %v", handled)
	}
	if pending, _ := a.ListPermissions(t.Context(), "s"); len(pending) != 1 || pending[0]["id"] != "p2" {
		t.Fatalf("pending = %+v", pending)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !f.called("POST", "/permissions/p1/reply", `"Reply":"reject"`) {
		if time.Now().After(deadline) {
			t.Fatalf("rule reply never sent: %+v", f.calls)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Allow always applies to the rest of the session.
	requireNoError(t, a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p2", Reply: "always"}))
	if got := a.localRules().Decide(asked("p3", "bash", "make")); got != "once" {
		t.Fatalf("after always = %q", got)
	}
}

func (f *fakeProvider) called(method, path, body string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.method == method && c.path == path && strings.Contains(string(c.body), body) {
			return true
		}
	}
	return false
}

func mustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

// A rule reply that fails is retried until delivered, and while it is in
// flight neither a reconciliation (the provider still lists the prompt) nor
// a browser stream exposes the prompt to the judge, a browser or the cache.
func TestRuleReplyFailureRetriesWithoutLeaking(t *testing.T) {
	old := ruleReplyBackoff
	ruleReplyBackoff = [2]time.Duration{time.Millisecond, 5 * time.Millisecond}
	t.Cleanup(func() { ruleReplyBackoff = old })
	f := newFake(t, allCapabilities())
	ask := asked("p1", "bash", "make")
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "id: 1\ndata: %s\n\n", mustJSON(providerprotocol.Event{Type: "permission.asked", Properties: mustJSON(ask)}))
	}
	release := make(chan struct{})
	failures := 0
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/permissions/p1/reply" {
			return false
		}
		f.mu.Lock()
		failures++
		n := failures
		f.mu.Unlock()
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			return true
		}
		<-release // delayed: the third attempt is in flight during the refresh
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	f.reply("GET", "/permissions", []platforms.LivePrompt{ask})
	a := f.adapter(t)
	a.SetRules(mustRules(t, ""))
	requireNoError(t, a.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s",
		Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "allow"}}}))
	var handled []string
	a.SetEventHandler(func(e providerprotocol.Event) { handled = append(handled, e.Type) })
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	_ = a.streamURL(ctx, "", 0, io.Discard, nil, "/events?consumer=headless&after=0", true)

	testutil.WaitFor(t, 5*time.Second, "three reply attempts", func() bool { f.mu.Lock(); defer f.mu.Unlock(); return failures >= 3 })
	refreshed, err := a.RefreshPermissions(t.Context(), "s")
	requireNoError(t, err)
	if len(refreshed) != 0 {
		t.Fatalf("reconciliation exposed the rule-decided prompt: %+v", refreshed)
	}
	if pending, _ := a.ListPermissions(t.Context(), "s"); len(pending) != 0 {
		t.Fatalf("pending = %+v", pending)
	}
	var browser bytes.Buffer
	_ = a.ProxyEvents(t.Context(), "s", &browser, nil)
	if strings.Contains(browser.String(), "p1") || len(handled) != 0 {
		t.Fatalf("prompt leaked: browser=%s handled=%v", browser.String(), handled)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	if failures != 3 {
		t.Fatalf("delivered reply was retried: %d attempts", failures)
	}
}

func mustRules(t *testing.T, path string) *Rules {
	t.Helper()
	r, err := OpenRules(path)
	requireNoError(t, err)
	return r
}

// An exact approval (providerprotocol.ExactPattern) matches only its call,
// while glob rules keep globbing; a reloaded store keeps the distinction.
func TestExactPatternsDoNotGlob(t *testing.T) {
	r, err := OpenRules(filepath.Join(t.TempDir(), "rules.json"))
	requireNoError(t, err)
	exact := func(permission, value string) platforms.PermissionRule {
		return platforms.PermissionRule{Permission: permission, Pattern: providerprotocol.ExactPattern(value), Action: "allow"}
	}
	requireNoError(t, r.approve("s", []platforms.PermissionRule{exact("bash", "echo *"), exact("edit", "/tmp/a?b"), exact("bash", `printf '\*'`)}))
	requireNoError(t, r.set("s", []platforms.PermissionRule{{Permission: "bash", Pattern: "git *", Action: "allow"}}))
	again, err := OpenRules(r.path)
	requireNoError(t, err)
	for _, store := range []*Rules{r, again} {
		for value, want := range map[[2]string]string{
			{"bash", "echo *"}: "once",
			{"bash", "echo harmless; touch /tmp/unapproved"}: "",
			{"edit", "/tmp/a?b"}:                             "once",
			{"edit", "/tmp/axb"}:                             "",
			{"bash", `printf '\*'`}:                          "once",
			{"bash", `printf '\x'`}:                          "",
			{"bash", "git status"}:                           "once",
			{"bash", "git"}:                                  "once",
		} {
			prompt := asked("1", value[0], providerprotocol.ExactPattern(value[1]))
			if got := store.Decide(prompt); got != want {
				t.Errorf("%v = %q, want %q", value, got, want)
			}
		}
	}
}

// An Allow-always saved before its reply grants nothing until the outcome
// is known, survives a reload while pending, and is granted or dropped by
// the reply claude received.
func TestTentativeAllowAlways(t *testing.T) {
	r, err := OpenRules(filepath.Join(t.TempDir(), "rules.json"))
	requireNoError(t, err)
	rule := []platforms.PermissionRule{{Permission: "bash", Pattern: "make", Action: "allow"}}
	requireNoError(t, r.approveTentatively("s", "p1", rule))
	requireNoError(t, r.approveTentatively("s", "p1", rule)) // a retried always
	requireNoError(t, r.approveTentatively("s", "p2", rule))
	if got := r.Decide(asked("1", "bash", "make")); got != "" {
		t.Fatalf("a tentative approval granted %q", got)
	}
	again, err := OpenRules(r.path)
	requireNoError(t, err)
	requireNoError(t, again.settle("p2", false))
	requireNoError(t, again.settle("missing", true))
	if got := again.Decide(asked("1", "bash", "make")); got != "" {
		t.Fatalf("a dropped approval granted %q", got)
	}
	requireNoError(t, again.settle("p1", true))
	requireNoError(t, again.settle("p1", false)) // settled once
	if got := again.Decide(asked("1", "bash", "make")); got != "once" {
		t.Fatalf("a confirmed approval = %q", got)
	}
	if len(again.data.Tentative) != 0 || len(again.data.Approved["s"]) != 1 {
		t.Fatalf("store = %+v", again.data)
	}
}
