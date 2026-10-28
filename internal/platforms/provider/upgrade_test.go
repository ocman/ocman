package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

// A version-1 supervisor that survived the upgrade (draining until idle)
// sends raw tool arguments as patterns. Its `echo *` must stay that exact
// command everywhere: the handler (judge, audit), the pending list, browsers
// and Allow-always.
func TestLegacySupervisorPatternsAreExact(t *testing.T) {
	for _, version := range []int{1, providerprotocol.CurrentVersion} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := newFake(t, allCapabilities())
			f.hello.ProtocolVersion = version
			raw := "echo *"
			if version >= providerprotocol.ExactPatternsVersion {
				raw = providerprotocol.ExactPattern("echo *") // a current supervisor escapes
			}
			ask := asked("p1", "bash", raw)
			ask["always"] = []any{raw}
			f.events = func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "id: 1\ndata: %s\n\n", mustJSON(providerprotocol.Event{Type: "permission.asked", Properties: mustJSON(ask)}))
			}
			f.reply("GET", "/permissions", []platforms.LivePrompt{ask})
			f.reply("POST", "/permissions/p1/reply", nil)
			connect := New
			if version < providerprotocol.PreviousVersion {
				connect = NewDraining
			}
			a, err := connect(t.Context(), f.socket)
			requireNoError(t, err)
			t.Cleanup(a.Close)
			if a.ReplyOutcomes(t.Context(), "s") != (version >= providerprotocol.ReplyOutcomeVersion) {
				t.Fatal("reply outcome support misreported")
			}
			a.SetRules(mustRules(t, filepath.Join(t.TempDir(), "rules.json")))
			var handled []string
			a.SetEventHandler(func(e providerprotocol.Event) {
				var p platforms.LivePrompt
				_ = json.Unmarshal(e.Properties, &p)
				handled = append(handled, fmt.Sprint(p["patterns"], p["always"]))
			})
			var browser bytes.Buffer
			_ = a.streamURL(t.Context(), "", 0, &browser, nil, "/events?consumer=headless&after=0", false)
			want := `[echo \*] [echo \*]`
			if len(handled) != 1 || handled[0] != want {
				t.Fatalf("handler saw %v, want %s", handled, want)
			}
			if !strings.Contains(browser.String(), `echo \\*`) {
				t.Fatalf("browser saw a raw pattern: %s", browser.String())
			}
			refreshed, err := a.RefreshPermissions(t.Context(), "s")
			requireNoError(t, err)
			if len(refreshed) != 1 || fmt.Sprint(refreshed[0]["patterns"]) != `[echo \*]` {
				t.Fatalf("refreshed = %+v", refreshed)
			}
			requireNoError(t, a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p1", Reply: "always"}))
			r := a.localRules()
			if got := r.Decide(asked("p2", "bash", providerprotocol.ExactPattern("echo harmless; touch /tmp/unapproved"))); got != "" {
				t.Fatalf("the approval globbed: %q", got)
			}
			if got := r.Decide(asked("p3", "bash", providerprotocol.ExactPattern("echo *"))); got != "once" {
				t.Fatalf("the approved call = %q", got)
			}
		})
	}
}

// Saved approvals stay exact across restarts; explicit rules keep their globs.
func TestExactRulesStoreApprovalsSurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	requireNoError(t, os.WriteFile(path, []byte(`{
		"exactPatterns":true,"exactRules":true,
		"rules":{"s":[{"permission":"bash","pattern":"git *","action":"allow"}]},
		"approved":{"s":[{"permission":"bash","pattern":"echo \\*","action":"allow"},{"permission":"todowrite","pattern":"\\*","action":"allow"}]},
		"tentative":{"p9":{"session":"s","rules":[{"permission":"edit","pattern":"/tmp/a\\*","action":"allow"}]}}}`), 0o600))
	for range 2 {
		r := mustRules(t, path)
		for value, want := range map[[2]string]string{
			{"bash", "echo *"}: "once",
			{"bash", "echo harmless; touch /tmp/unapproved"}: "",
			{"bash", "git status"}:                           "once", // explicit glob rule
			{"todowrite", "*"}:                               "once", // whole-tool approval
		} {
			if got := r.Decide(asked("1", value[0], providerprotocol.ExactPattern(value[1]))); got != want {
				t.Errorf("%v = %q, want %q", value, got, want)
			}
		}
		requireNoError(t, r.settle("p9", true))
		if got := r.Decide(asked("2", "edit", "/tmp/anything")); got != "" {
			t.Errorf("tentative legacy approval globbed: %q", got)
		}
	}
}

// The rules store accepts the tentative Allow-always, then fails before the
// outcome is recorded. Claude has the reply; the turn finishes. The outcome
// is not lost: the headless stream stops at permission.replied (the turn
// stays unacknowledged, its events replayable) until the store works, and
// after a restart the replayed reply promotes the grant.
func TestAllowAlwaysOutcomeSurvivesStoreFailure(t *testing.T) {
	caps := allCapabilities()
	caps.Replay = true
	f := newFake(t, caps)
	path := filepath.Join(t.TempDir(), "rules.json")
	ask := asked("p1", "bash", "make")
	events := []providerprotocol.Event{
		{Type: "permission.asked", Properties: mustJSON(ask)},
		{Type: "permission.replied", Properties: mustJSON(map[string]any{"sessionID": "s", "requestID": "p1", "reply": "always"})},
		{Type: "session.idle", Properties: mustJSON(map[string]any{"sessionID": "s"})},
	}
	var available atomic.Int32 // events the supervisor has published so far
	f.events = func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		after := 0
		_, _ = fmt.Sscan(r.URL.Query().Get("after"), &after)
		for i, e := range events[after:available.Load()] {
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", after+i+1, mustJSON(e))
		}
	}
	broken := path + ".tmp"
	f.hook = func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path == "/permissions/p1/reply" { // the tentative grant is saved; the store now fails
			requireNoError(t, os.MkdirAll(filepath.Join(broken, "x"), 0o700))
		}
		return false
	}
	a := f.adapter(t)
	a.SetRules(mustRules(t, path))
	stream := func(a *Adapter) error {
		return a.streamURL(t.Context(), "", a.seq, io.Discard, nil, fmt.Sprintf("/events?consumer=headless&after=%d", a.seq), true)
	}
	available.Store(1)
	_ = stream(a)
	requireNoError(t, a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p1", Reply: "always"}))
	available.Store(3) // claude received always; the turn finished
	if err := stream(a); err == nil {
		t.Fatal("the stream moved past an outcome the store could not record")
	}
	if a.seq != 1 || f.called("POST", "/sessions/s/ack", "") {
		t.Fatalf("cursor %d advanced or the turn was acknowledged", a.seq)
	}
	if got := a.localRules().Decide(asked("p2", "bash", "make")); got != "" {
		t.Fatalf("an unrecorded grant took effect: %q", got)
	}

	requireNoError(t, os.RemoveAll(broken)) // storage restored; ocman restarts
	again := f.adapter(t)
	again.SetRules(mustRules(t, path))
	_ = stream(again)
	if got := again.localRules().Decide(asked("p2", "bash", "make")); got != "once" {
		t.Fatalf("the delivered Allow always was lost: %q", got)
	}
	if !f.called("POST", "/sessions/s/ack", `"sequence":3`) {
		t.Fatal("the turn was never acknowledged")
	}
}
