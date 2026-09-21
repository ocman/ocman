package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/state"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
	"github.com/NoUseFreak/ocman/sdk/plugin/conformance"
)

// These tests are the acceptance walkthrough for the bundled Slack plugin,
// driven against a mock workspace: the real executable, the real host, and
// deterministic stand-ins for Slack's two Web API methods and its Socket Mode
// stream. They prove the wiring, not the live workspace — a real Slack app,
// its tokens and a channel invite still have to be checked by hand.

const (
	slackPluginID   = "org.ocman.slack"
	slackTestTeam   = "T0ACCEPT"
	slackTestChan   = "C1"
	slackTestUser   = "U123"
	slackThreadRoot = "1700000000.000100"
)

func slackTestThread() string { return slackTestChan + ":" + slackThreadRoot }

// buildSlackPlugin builds the real executable exactly as
// `make install-plugin PLUGIN=slack` does, so what is verified here is the
// binary an operator installs.
func buildSlackPlugin(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ocman-plugin-slack")
	out, err := exec.Command("go", "build", "-trimpath", "-o", path, "../../examples/ocman-plugin-slack").CombinedOutput()
	if err != nil {
		t.Fatalf("building the slack plugin: %s: %v", out, err)
	}
	return path
}

// installSlackPlugin installs the executable under dir behind a wrapper that
// points its Slack calls at the mock workspace. ocman fixes a plugin's argv,
// environment and fd 3, so a wrapper is the only place a test can inject the
// mock's address; configPath, when set, supplies configuration on fd 3 the way
// the host normally does.
func installSlackPlugin(t *testing.T, dir, binary, api, configPath string) string {
	t.Helper()
	redirect := ""
	if configPath != "" {
		redirect = fmt.Sprintf(" 3<%q", configPath)
	}
	script := fmt.Sprintf("#!/bin/sh\nexport OCMAN_SLACK_API=%q\nexec %q \"$@\"%s\n", api, binary, redirect)
	path := filepath.Join(dir, "ocman-plugin-slack")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func slackTestConfiguration(t *testing.T, project string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "configuration.json")
	data := fmt.Sprintf(`{"project":%q,"appToken":"xapp-test","botToken":"xoxb-test","allowedUsers":%q}`+"\n",
		project, slackTestUser)
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// slackWorkspace is a deterministic stand-in for one Slack workspace: the two
// Web API methods the plugin calls plus one Socket Mode connection.
type slackWorkspace struct {
	*httptest.Server
	send  chan any
	acks  chan string
	posts chan map[string]string
}

func newSlackWorkspace(t *testing.T) *slackWorkspace {
	t.Helper()
	w := &slackWorkspace{send: make(chan any, 8), acks: make(chan string, 8), posts: make(chan map[string]string, 8)}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/apps.connections.open", func(rw http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer xapp-test" {
			_ = json.NewEncoder(rw).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		_ = json.NewEncoder(rw).Encode(map[string]any{
			"ok": true, "url": "ws" + strings.TrimPrefix(w.URL, "http") + "/link",
		})
	})
	mux.HandleFunc("/chat.postMessage", func(rw http.ResponseWriter, r *http.Request) {
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if r.Header.Get("Authorization") != "Bearer xoxb-test" {
			_ = json.NewEncoder(rw).Encode(map[string]any{"ok": false, "error": "invalid_auth"})
			return
		}
		w.posts <- body
		_ = json.NewEncoder(rw).Encode(map[string]any{"ok": true})
	})
	mux.HandleFunc("/link", func(rw http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for frame := range w.send {
			if conn.WriteJSON(frame) != nil {
				return
			}
			var ack map[string]string
			if conn.ReadJSON(&ack) != nil {
				return
			}
			w.acks <- ack["envelope_id"]
		}
	})
	w.Server = httptest.NewServer(mux)
	t.Cleanup(w.Close)
	return w
}

// mention delivers one authorized @mention and waits for the plugin's
// acknowledgment, so a test never races the Socket Mode stream. threadTS is
// empty for a message that starts its own thread.
func (w *slackWorkspace) mention(t *testing.T, eventID, ts, threadTS, text string) {
	t.Helper()
	event := map[string]any{
		"type": "app_mention", "user": slackTestUser, "text": "<@U0BOT> " + text,
		"ts": ts, "channel": slackTestChan,
	}
	if threadTS != "" {
		event["thread_ts"] = threadTS
	}
	payload, err := json.Marshal(map[string]any{"team_id": slackTestTeam, "event_id": eventID, "event": event})
	if err != nil {
		t.Fatal(err)
	}
	w.send <- map[string]any{"type": "events_api", "envelope_id": eventID, "payload": json.RawMessage(payload)}
	select {
	case <-w.acks:
	case <-time.After(30 * time.Second):
		t.Fatalf("the plugin never acknowledged %s", eventID)
	}
}

func (w *slackWorkspace) awaitPost(t *testing.T) map[string]string {
	t.Helper()
	select {
	case post := <-w.posts:
		return post
	case <-time.After(30 * time.Second):
		t.Fatal("no reply reached the slack thread")
		return nil
	}
}

func (w *slackWorkspace) noFurtherPost(t *testing.T) {
	t.Helper()
	time.Sleep(300 * time.Millisecond)
	if len(w.posts) != 0 {
		t.Fatalf("an extra reply was posted: %+v", <-w.posts)
	}
}

// installSlack drives the operator's own steps over the real endpoints:
// rescan, configure the project and both tokens, then enable with the
// conversation grant approved.
func (f *conversationFixture) installSlack(t *testing.T) {
	t.Helper()
	f.call(t, "POST", "/rescan", `{}`, 200)
	configuration, err := json.Marshal(pluginManagementInput{
		Values: map[string]json.RawMessage{
			"project":      mustJSON(t, f.project),
			"allowedUsers": mustJSON(t, slackTestUser),
		},
		Secrets: map[string]string{"appToken": "xapp-test", "botToken": "xoxb-test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+slackPluginID+"/configuration", string(configuration), 200)
	p, err := f.s.stateDB.GetPlugin(t.Context(), slackPluginID)
	if err != nil {
		t.Fatal(err)
	}
	grants := []string{plugins.ConversationSessionGrant}
	enable, err := json.Marshal(pluginManagementInput{Approval: p.Approval, Grants: &grants})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+slackPluginID+"/enable", string(enable), 200)
}

func (f *conversationFixture) awaitQueued(t *testing.T, want int) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		queued, err := f.s.queueSvc().List(t.Context(), "opencode", "ses-chat")
		if err != nil {
			t.Fatal(err)
		}
		if len(queued) >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d held messages, saw %d", want, len(queued))
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestSlackPluginDescribesAndConforms validates the real executable against the
// runtime it will be installed into: the host's own scanner runs its describe
// handshake, and the SDK's conformance suite checks the conversation.v1
// declaration contract plus grant and project denials.
func TestSlackPluginDescribesAndConforms(t *testing.T) {
	workspace := newSlackWorkspace(t)
	binary := buildSlackPlugin(t)

	dir := t.TempDir()
	installSlackPlugin(t, dir, binary, workspace.URL, "")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entries, err := plugins.Scan(ctx, dir, nil)
	if err != nil || len(entries) != 1 || entries[0].Err != nil {
		t.Fatalf("discovery: %+v %v", entries, err)
	}
	d := entries[0].Description
	if d.ID != slackPluginID || d.Scope != plugins.ScopeOwner {
		t.Fatalf("declaration: %q %q", d.ID, d.Scope)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("declaration does not satisfy the host contract: %v", err)
	}
	capability := false
	for _, c := range d.Capabilities {
		capability = capability || c.Name == plugins.ConversationCapability.Name
	}
	if !capability || len(d.RequestedGrants) != 1 || d.RequestedGrants[0] != plugins.ConversationSessionGrant {
		t.Fatalf("capabilities %+v grants %+v", d.Capabilities, d.RequestedGrants)
	}
	// The scopes an operator grants the Slack app are worth nothing if the
	// tokens carrying them are not write-only secrets.
	for _, s := range d.Settings {
		wantSecret := s.Key == "appToken" || s.Key == "botToken"
		if s.Secret != wantSecret {
			t.Fatalf("setting %q secret=%v", s.Key, s.Secret)
		}
		if s.Key == plugin.ConversationProjectSetting && !s.Required {
			t.Fatal("the one reachable project must be a required setting")
		}
	}

	// The conformance launcher supplies no configuration of its own, so the
	// wrapper hands the plugin a test configuration on fd 3.
	const project = "/conformance/project"
	executable := installSlackPlugin(t, t.TempDir(), binary, workspace.URL, slackTestConfiguration(t, project))
	conformance.RunConversationGrants(t, executable, project)
	// The granted reply of that suite is the one post it makes.
	if post := workspace.awaitPost(t); post["text"] != "conformance reply" {
		t.Fatalf("conformance reply: %+v", post)
	}
}

// TestSlackPluginEndToEnd is the acceptance walkthrough against a mock
// workspace: a first @mention starts a session in the approved project, a
// follow-up arriving mid-turn is held rather than interleaved, the completed
// answer is posted back into the originating thread, and a host with no memory
// of either continues the conversation and delivers a reply owed from before.
func TestSlackPluginEndToEnd(t *testing.T) {
	workspace := newSlackWorkspace(t)
	f := newConversationFixture(t)
	// The walkthrough covers the real plugin alone.
	if err := os.Remove(filepath.Join(f.pluginDir, "ocman-plugin-chatops")); err != nil {
		t.Fatal(err)
	}
	installSlackPlugin(t, f.pluginDir, buildSlackPlugin(t), workspace.URL, "")
	f.installSlack(t)

	// First mention: one session, in the configured project, with the
	// addressing mention stripped from the prompt.
	workspace.mention(t, "Ev1first", slackThreadRoot, "", "ship it")
	if prompts := f.awaitPrompts(t, 1); prompts[0] != "ship it" {
		t.Fatalf("first prompt %q", prompts[0])
	}
	if created := f.createdSessions(); created != 1 {
		t.Fatalf("sessions created: %d", created)
	}

	// A follow-up mention while the turn is running is held, not interleaved:
	// nobody in the thread can see that a turn is in flight.
	f.setBusy(true)
	workspace.mention(t, "Ev2follow", "1700000000.000200", slackThreadRoot, "and deploy it")
	f.awaitQueued(t, 1)
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 1 {
		t.Fatalf("a mention interleaved into a running turn: %v", prompts)
	}

	// The idle edge drains the follow-up onto the same session and records the
	// completed answer for delivery.
	f.setBusy(false)
	f.settle(t)
	prompts := f.awaitPrompts(t, 2)
	if prompts[1] != "and deploy it" {
		t.Fatalf("follow-up prompt %q", prompts[1])
	}
	for _, target := range f.sentTo() {
		if target != "ses-chat" {
			t.Fatalf("the thread's messages split across sessions: %v", f.sentTo())
		}
	}
	f.pump(t, f.s)
	post := workspace.awaitPost(t)
	if post["channel"] != slackTestChan || post["thread_ts"] != slackThreadRoot || post["text"] != "first half\n\nsecondhalf" {
		t.Fatalf("reply posted as %+v", post)
	}
	// A repeated idle edge for the same completed turn posts nothing more.
	f.settle(t)
	f.pump(t, f.s)
	workspace.noFurtherPost(t)

	// Restart recovery, inbound: a host with none of the previous process's
	// memory continues the thread's session instead of starting a second one.
	restarted := f.restart(t)
	if err := restarted.startConversation(t.Context(), slackPluginID, f.project, plugins.ConversationMessage{
		AccountID: slackTestTeam, ThreadID: slackTestThread(), EventID: "Ev3restart", Text: "still here?",
	}); err != nil {
		t.Fatal(err)
	}
	if created := f.createdSessions(); created != 1 {
		t.Fatalf("restart created a second session for the thread: %d", created)
	}
	if prompts := f.awaitPrompts(t, 3); prompts[2] != "still here?" {
		t.Fatalf("prompt after restart %q", prompts[2])
	}

	// Restart recovery, outbound: a reply recorded before the restart is still
	// delivered, with the outbox row's id as its operation id.
	if _, err := f.s.stateDB.AppendPluginConversationReply(t.Context(), state.PluginConversationKey{
		PluginID: slackPluginID, AccountID: slackTestTeam, ThreadID: slackTestThread(),
	}, "ses-chat:m9", "recorded before the restart"); err != nil {
		t.Fatal(err)
	}
	f.pump(t, f.restart(t))
	if post := workspace.awaitPost(t); post["text"] != "recorded before the restart" {
		t.Fatalf("reply owed across a restart: %+v", post)
	}
	if status := f.backlogFor(t, slackPluginID); status.Pending != 0 || status.Dead != 0 {
		t.Fatalf("delivered replies stayed in the backlog: %+v", status)
	}

	// Neither token may surface in diagnostics.
	stderr := f.call(t, "GET", "/"+slackPluginID+"/stderr", "", 200)
	for _, secret := range []string{"xapp-test", "xoxb-test"} {
		if strings.Contains(stderr, secret) {
			t.Fatalf("%s leaked into diagnostics", secret)
		}
	}
}
