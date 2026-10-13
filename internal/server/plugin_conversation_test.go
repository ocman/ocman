package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	"github.com/NoUseFreak/ocman/internal/state/statetest"
	"github.com/NoUseFreak/ocman/internal/testutil"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

const (
	conversationTestThread  = "slackC1:1700000000.000100"
	conversationTestAccount = "T0WORKSPACE"
	conversationTestEvent   = "Ev0trigger"
)

func conversationPluginDescription() plugin.Description {
	return plugin.Description{
		ID: "org.example.chatops", Name: "Chat Ops", Version: "1", Protocol: plugin.Version{Major: 1},
		Scope: plugin.ScopeOwner, MaxConcurrency: 2,
		Capabilities:    []plugin.Capability{plugin.ConversationCapability},
		RequestedGrants: []string{plugin.ConversationSessionGrant},
		Settings: []plugin.Setting{
			{Key: plugin.ConversationProjectSetting, Label: "Project", Type: "string", Required: true},
			{Key: "trigger", Label: "Trigger text", Type: "string"},
			{Key: "failThread", Label: "Thread whose replies fail", Type: "string"},
			{Key: "token", Label: "Provider token", Type: "string", Secret: true},
		},
	}
}

// TestConversationPluginHelper is a real plugin executable, re-exec'd by a
// shell shim. It stands in for a provider: on serve it emits one normalized
// message and records every completed reply the host sends back.
func TestConversationPluginHelper(t *testing.T) {
	if len(os.Args) != 5 || os.Args[2] != "--" {
		return
	}
	record, mode := os.Args[3], plugin.Mode(os.Args[4])
	d := conversationPluginDescription()
	var handler plugin.Handler
	var events chan plugin.Event
	if mode == plugin.ModeServe {
		f := os.NewFile(3, "config")
		line, _ := bufio.NewReader(io.LimitReader(f, plugin.MaxMessageBytes)).ReadBytes('\n')
		_ = f.Close()
		var cfg struct {
			Trigger    string `json:"trigger"`
			FailThread string `json:"failThread"`
			Token      string `json:"token"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(line), &cfg)
		// Every attempt is recorded with its operation id, so a test can see
		// both that a delivery was replayed and that its identity was stable.
		handler = plugin.ConversationHandler(func(_ context.Context, operationID string, r plugin.ConversationReply) error {
			kind := "reply"
			if cfg.FailThread != "" && r.ThreadID == cfg.FailThread {
				kind = "fail"
			}
			out, _ := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", kind, r.ThreadID, operationID, strings.ReplaceAll(r.Text, "\n", "\\n"))
			_ = out.Close()
			if kind == "fail" {
				return plugin.Failure(plugin.ErrorUnavailable)
			}
			return nil
		})
		events = make(chan plugin.Event, 4)
		if cfg.Trigger != "" {
			message, err := plugin.NewConversationMessage(plugin.ConversationMessage{
				AccountID: conversationTestAccount, ThreadID: conversationTestThread,
				EventID: conversationTestEvent, Text: cfg.Trigger,
			})
			if err == nil {
				events <- message
			}
		}
	}
	_ = plugin.RunWithEvents(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler, events)
	// Exit before the test framework writes its own summary: stdout carries
	// NDJSON only, and a coverage-instrumented build would otherwise append to it.
	os.Exit(0)
}

type conversationTestHost struct{ hostsvc.Host }

func (*conversationTestHost) RemoteID() string { return "local" }
func (*conversationTestHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
	return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: req.ProjectDir}, nil
}

type conversationFixture struct {
	s         *Server
	mux       *http.ServeMux
	cookie    *http.Cookie
	record    string
	project   string
	pluginDir string
	// failThread configures the plugin to reject replies for one thread, so a
	// delivery failure can be driven without breaking the plugin process.
	failThread string

	mu       sync.Mutex
	prompts  []string
	targets  []string
	sessions int
	busy     bool
	// errored reports the session's last turn as failed, so the outcome notice
	// path can be driven without synthesizing an error message shape.
	errored bool
	// createErr makes session creation fail, standing in for an agent the host
	// cannot reach at all.
	createErr error
}

// sessionID names the Nth created session. The first keeps the plain name so a
// test can address it without knowing how many were created.
func conversationSessionID(n int) string {
	if n <= 1 {
		return "ses-chat"
	}
	return fmt.Sprintf("ses-chat-%d", n)
}

func (f *conversationFixture) setBusy(busy bool) {
	f.mu.Lock()
	f.busy = busy
	f.mu.Unlock()
}

func (f *conversationFixture) sentTo() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...)
}

func (f *conversationFixture) createdSessions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sessions
}

func newConversationFixture(t *testing.T) *conversationFixture {
	t.Helper()
	pluginDir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", pluginDir)
	stateDB, err := state.Open(statetest.Path(t), ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })

	f := &conversationFixture{
		record:    filepath.Join(t.TempDir(), "replies"),
		project:   mustEvalSymlinks(t, t.TempDir()),
		pluginDir: pluginDir,
	}
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{
		id:       "opencode",
		sessions: []db.Session{{ID: "ses-chat"}},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.createErr != nil {
				return nil, f.createErr
			}
			f.sessions++
			if req.Directory != f.project {
				t.Errorf("session created outside the approved project: %q", req.Directory)
			}
			return &platforms.CreateSessionResponse{ID: conversationSessionID(f.sessions)}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.prompts = append(f.prompts, req.Message)
			f.targets = append(f.targets, req.SessionID)
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			f.mu.Lock()
			status := db.StatusDone
			if f.busy {
				status = db.StatusBusy
			}
			if f.errored {
				status = db.StatusError
			}
			f.mu.Unlock()
			if id == "" {
				id = "ses-chat"
			}
			return &platforms.SessionDetail{
				Session: &db.Session{ID: id, Directory: f.project, Status: status},
				Messages: []db.Message{
					{ID: "m1", TimeCreated: 100, Data: json.RawMessage(`{"role":"user"}`)},
					{ID: "m2", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
				},
				Parts: []db.Part{
					{ID: "p0", MessageID: "m1", Data: json.RawMessage(`{"type":"text","text":"the prompt"}`)},
					// The live adapter leaves TimeCreated zero, so arrival order decides.
					{ID: "zz", MessageID: "m2", Data: json.RawMessage(`{"type":"text","text":"first half"}`)},
					{ID: "aa", MessageID: "m2", Data: json.RawMessage(`{"type":"text","text":"second\bhalf"}`)},
					{ID: "p3", MessageID: "m2", Data: json.RawMessage(`{"type":"reasoning","text":"private"}`)},
				},
			}, nil
		},
	})
	f.s = &Server{
		stateDB: stateDB, auth: newTestAuth(t, "password"),
		registry: registry, hostRouter: hostsvc.NewRouter(&conversationTestHost{}),
	}
	f.s.sessions = sessionsvc.New(registry, sessionsvc.Hooks{})
	bindPlugins(f.s, context.Background())
	t.Cleanup(f.s.stopPluginProcesses)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestConversationPluginHelper$ -- %q \"$@\"\n", exe, f.record)
	testutil.WriteScript(t, filepath.Join(pluginDir, "ocman-plugin-chatops"), script)
	if f.mux, err = f.s.routes(); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.s.auth.issueCookie(w, httptest.NewRequest(http.MethodGet, "/", nil))
	f.cookie = w.Result().Cookies()[0]
	return f
}

func mustEvalSymlinks(t *testing.T, dir string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func (f *conversationFixture) call(t *testing.T, method, path, body string, status int) string {
	t.Helper()
	r := httptest.NewRequest(method, "http://localhost:8228/api/plugins"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.AddCookie(f.cookie)
	w := httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	if w.Code != status {
		t.Fatalf("%s %s: %d want %d: %s", method, path, w.Code, status, w.Body.String())
	}
	return w.Body.String()
}

// install drives the real Settings endpoints: rescan, configure, enable.
func (f *conversationFixture) install(t *testing.T, project string, grants []string) {
	t.Helper()
	id := conversationPluginDescription().ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	values := map[string]json.RawMessage{"project": mustJSON(t, project), "trigger": mustJSON(t, "ship it")}
	if f.failThread != "" {
		values["failThread"] = mustJSON(t, f.failThread)
	}
	configuration, err := json.Marshal(remote.PluginInput{
		Values:  values,
		Secrets: map[string]string{"token": "xoxb-super-secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+id+"/configuration", string(configuration), 200)
	p, err := f.s.stateDB.GetPlugin(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	enable, err := json.Marshal(remote.PluginInput{Approval: p.Approval, Grants: &grants})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+id+"/enable", string(enable), 200)
}

func mustJSON(t *testing.T, value string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f *conversationFixture) awaitPrompts(t *testing.T, want int) []string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		f.mu.Lock()
		prompts := append([]string(nil), f.prompts...)
		f.mu.Unlock()
		if len(prompts) >= want {
			return prompts
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected %d prompts, saw %v", want, prompts)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *conversationFixture) replies() string {
	data, _ := os.ReadFile(f.record)
	return string(data)
}

// TestConversationPluginRoundTrip is the whole path: a plugin-initiated
// normalized message starts a managed session in its approved project, and the
// session's completed assistant reply comes back in the originating thread.
func TestConversationPluginRoundTrip(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})

	prompts := f.awaitPrompts(t, 1)
	if prompts[0] != "ship it" {
		t.Fatalf("prompt %q", prompts[0])
	}
	f.mu.Lock()
	created := f.sessions
	f.mu.Unlock()
	if created != 1 {
		t.Fatalf("sessions created: %d", created)
	}

	f.s.onSessionIdle("opencode", "ses-chat")
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(f.replies(), "reply\t") {
		if time.Now().After(deadline) {
			t.Fatalf("no reply reached the thread: %q", f.replies())
		}
		time.Sleep(10 * time.Millisecond)
	}
	// The control character in the assistant text is stripped, not dropped. The
	// operation id is the outbox row's immutable id, the first in a fresh
	// database, and is what a provider adapter keys its own idempotency on.
	want := "reply\t" + conversationTestThread + "\tconv-out:1\tfirst half\\n\\nsecondhalf\n"
	if got := f.replies(); got != want {
		t.Fatalf("reply %q, want %q", got, want)
	}

	// A repeated idle edge for the same completed turn must not post twice.
	f.s.onSessionIdle("opencode", "ses-chat")
	time.Sleep(200 * time.Millisecond)
	if got := f.replies(); got != want {
		t.Fatalf("duplicate reply posted: %q", got)
	}

	// Secrets never surface in diagnostics.
	stderr := f.call(t, "GET", "/"+conversationPluginDescription().ID+"/stderr", "", 200)
	if strings.Contains(stderr, "xoxb-super-secret") {
		t.Fatal("secret leaked into diagnostics")
	}
}

func TestConversationPluginDeniedWithoutGrant(t *testing.T) {
	f := newConversationFixture(t)
	// Enabling with an incomplete grant set is refused outright, so the only
	// reachable ungranted state is a revocation after enablement.
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	if err := f.s.stateDB.SetPluginGrants(t.Context(), conversationPluginDescription().ID, nil); err != nil {
		t.Fatal(err)
	}
	event := conversationMessageEvent(t, conversationMessage("again"))
	err := f.s.pluginsModule().Conversations().Deliver(t.Context(), conversationPluginDescription().ID, event)
	assertConversationDenied(t, err)
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 1 {
		t.Fatalf("ungranted message reached a session: %v", prompts)
	}
	// A revoked grant also blocks the outbound reply.
	assertConversationDenied(t, f.s.pluginsModule().Conversations().Reply(t.Context(), conversationPluginDescription().ID, "op-1",
		plugins.ConversationReply{AccountID: conversationTestAccount, ThreadID: conversationTestThread, Text: "leak"}))
}

func TestConversationPluginDeniedForUnapprovedProject(t *testing.T) {
	f := newConversationFixture(t)
	missing := filepath.Join(f.project, "not-a-project")
	f.install(t, missing, []string{plugins.ConversationSessionGrant})
	id := conversationPluginDescription().ID

	// A project the host cannot resolve never produces a session.
	err := f.s.pluginsModule().Conversations().Deliver(t.Context(), id, conversationMessageEvent(t, conversationMessage("go")))
	assertConversationDenied(t, err)

	// Nor does a plugin naming a project other than its configured one.
	claim := conversationMessage("go")
	claim.Project = f.project
	assertConversationDenied(t, f.s.pluginsModule().Conversations().Deliver(t.Context(), id, conversationMessageEvent(t, claim)))
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 0 {
		t.Fatalf("unapproved project started a session: %v", prompts)
	}
}

func TestConversationPluginDisabledIsUnavailable(t *testing.T) {
	f := newConversationFixture(t)
	f.call(t, "POST", "/rescan", `{}`, 200)
	err := f.s.pluginsModule().Conversations().Deliver(t.Context(), conversationPluginDescription().ID,
		conversationMessageEvent(t, conversationMessage("go")))
	if err == nil {
		t.Fatal("a disabled plugin reached session orchestration")
	}
}

// settle fires the idle edge for the thread's session, so the next message is
// not merely held behind the turn the previous one started.
func (f *conversationFixture) settle(t *testing.T) {
	t.Helper()
	f.s.onSessionIdle("opencode", "ses-chat")
	if err := f.s.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// deliver runs one inbound message straight through the broker, bypassing the
// per-plugin worker. Continuity must not depend on that serialization.
func (f *conversationFixture) deliver(t *testing.T, m plugins.ConversationMessage) error {
	t.Helper()
	return f.s.pluginsModule().Conversations().Deliver(t.Context(), conversationPluginDescription().ID, conversationMessageEvent(t, m))
}

// restart stands in for an ocman restart: a brand new Server over the same
// durable state, with none of the previous process's memory.
func (f *conversationFixture) restart(t *testing.T) *Server {
	t.Helper()
	s := &Server{
		stateDB: f.s.stateDB, auth: f.s.auth,
		registry: f.s.registry, hostRouter: hostsvc.NewRouter(&conversationTestHost{}),
	}
	s.sessions = sessionsvc.New(f.s.registry, sessionsvc.Hooks{})
	// The plugin process is relaunched by the new host at startup; what
	// this Server does not have is any memory of work in flight.
	bindPlugins(s, context.Background()).pluginsModule().AdoptProcesses(f.s.pluginsModule())
	return s
}

// TestConversationDuplicateDeliveryIsIgnored covers a provider redelivering the
// same event: neither a second session nor a second prompt may result.
func TestConversationDuplicateDeliveryIsIgnored(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	f.settle(t)

	repeat := conversationMessage("do it twice")
	if err := f.deliver(t, repeat); err != nil {
		t.Fatal(err)
	}
	// Same event id: a retry of one delivery, not a new request.
	if err := f.deliver(t, repeat); err != nil {
		t.Fatalf("a duplicate delivery must be dropped, not failed: %v", err)
	}
	prompts := f.awaitPromptsNoGrowth(t)
	if len(prompts) != 2 || prompts[1] != "do it twice" {
		t.Fatalf("duplicate delivery produced %v", prompts)
	}
	// A duplicate must be dropped outright, not merely held: a copy left in the
	// queue would be a second prompt on the next idle edge.
	queued, err := f.s.queueSvc().List(t.Context(), "opencode", "ses-chat")
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Fatalf("duplicate delivery left %d queued messages", len(queued))
	}
	if created := f.createdSessions(); created != 1 {
		t.Fatalf("sessions created: %d, want 1", created)
	}
}

// TestConversationConcurrentFirstMessagesMapOneSession drives concurrent first
// messages for one thread through the broker directly, so the in-process worker
// serialization cannot be what makes the mapping single.
func TestConversationConcurrentFirstMessagesMapOneSession(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	const racers = 4
	var wg sync.WaitGroup
	errs := make([]error, racers)
	for i := range racers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := conversationMessage(fmt.Sprintf("racer %d", i))
			m.ThreadID = "slackC9:1700000000.000900"
			errs[i] = f.deliver(t, m)
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	mapped, ok, err := f.s.stateDB.GetPluginConversation(t.Context(), state.PluginConversationKey{
		PluginID: conversationPluginDescription().ID, AccountID: conversationTestAccount,
		ThreadID: "slackC9:1700000000.000900",
	})
	if err != nil || !ok {
		t.Fatalf("thread not mapped: %v %v", ok, err)
	}
	// Every racer's message has to end up on the one mapped session, whether it
	// was sent straight away or held behind the turn the first one started. A
	// second mapped session would silently split the conversation in two.
	landed := 0
	for _, target := range f.sentTo()[1:] {
		if target != mapped.SessionID {
			t.Fatalf("prompt sent to unmapped session %q, mapped %q", target, mapped.SessionID)
		}
		landed++
	}
	queued, err := f.s.queueSvc().List(t.Context(), mapped.PlatformID, mapped.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if landed+len(queued) != racers {
		t.Fatalf("%d sent and %d queued, want %d racer messages on the mapped session", landed, len(queued), racers)
	}
}

// TestConversationMappingSurvivesRestart proves the mapping is durable: a
// process with no memory of the thread still continues its session, and a turn
// completing after the restart still resolves back to the thread.
func TestConversationMappingSurvivesRestart(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)
	before := f.sentTo()

	restarted := f.restart(t)
	if err := restarted.pluginsModule().Conversations().Deliver(t.Context(), conversationPluginDescription().ID, conversationMessageEvent(t, conversationMessage("still here?"))); err != nil {
		t.Fatal(err)
	}
	if created := f.createdSessions(); created != 1 {
		t.Fatalf("restart created a second session for the thread: %d", created)
	}
	after := f.sentTo()
	if len(after) != len(before)+1 || after[len(after)-1] != before[0] {
		t.Fatalf("prompt after restart went to %v, want the mapped %q", after, before[0])
	}
	key, ok, err := restarted.stateDB.GetPluginConversationThread(t.Context(),
		state.PluginConversationSession{PlatformID: "opencode", SessionID: before[0]})
	if err != nil || !ok || key.ThreadID != conversationTestThread || key.AccountID != conversationTestAccount {
		t.Fatalf("completed turn cannot find its thread after a restart: %+v %v %v", key, ok, err)
	}
}

// TestConversationBusyTurnQueuesInOrder covers the external-message policy: a
// mention arriving mid-turn is held rather than interleaved, and the held
// messages drain one per turn in arrival order.
func TestConversationBusyTurnQueuesInOrder(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	f.setBusy(true)
	for _, text := range []string{"second", "third"} {
		if err := f.deliver(t, conversationMessage(text)); err != nil {
			t.Fatal(err)
		}
	}
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 1 {
		t.Fatalf("a message interleaved into a running turn: %v", prompts)
	}

	f.setBusy(false)
	f.settle(t)
	prompts := f.awaitPrompts(t, 2)
	if prompts[1] != "second" {
		t.Fatalf("queue drained out of order: %v", prompts)
	}
	// One follow-up per turn: the next message waits for the next idle edge.
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 2 {
		t.Fatalf("the whole backlog drained into one turn: %v", prompts)
	}
	f.settle(t)
	if prompts := f.awaitPrompts(t, 3); prompts[2] != "third" {
		t.Fatalf("queue drained out of order: %v", prompts)
	}
}

// TestConversationMappingIsolation covers the two ways a naive mapping merges
// unrelated conversations: a thread identity reused in another workspace, and
// two threads inside one workspace.
func TestConversationMappingIsolation(t *testing.T) {
	f := newConversationFixture(t)
	f.install(t, f.project, []string{plugins.ConversationSessionGrant})
	f.awaitPrompts(t, 1)

	// Same thread identity, different workspace: providers do not guarantee
	// thread ids are unique across accounts.
	otherWorkspace := conversationMessage("other workspace")
	otherWorkspace.AccountID = "T0OTHER"
	// Same workspace, different thread.
	otherThread := conversationMessage("other thread")
	otherThread.ThreadID = "slackC2:1700000000.000200"
	for _, m := range []plugins.ConversationMessage{otherWorkspace, otherThread} {
		if err := f.deliver(t, m); err != nil {
			t.Fatal(err)
		}
	}
	if created := f.createdSessions(); created != 3 {
		t.Fatalf("unrelated conversations shared a session: %d created", created)
	}
	targets := f.sentTo()
	if len(targets) != 3 {
		t.Fatalf("prompts %v", targets)
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target] {
			t.Fatalf("unrelated conversations mapped to the same session: %v", targets)
		}
		seen[target] = true
	}
}

func (f *conversationFixture) awaitPromptsNoGrowth(t *testing.T) []string {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
}

var conversationEventSeq atomic.Int64

// conversationMessage builds an inbound message for the canonical test thread
// with a fresh event id, so successive calls are distinct deliveries rather
// than duplicates of one.
func conversationMessage(text string) plugins.ConversationMessage {
	return plugins.ConversationMessage{
		AccountID: conversationTestAccount, ThreadID: conversationTestThread,
		EventID: fmt.Sprintf("Ev%d", conversationEventSeq.Add(1)),
		Text:    text,
	}
}

func conversationMessageEvent(t *testing.T, m plugins.ConversationMessage) plugins.Event {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return plugins.Event{Capability: plugins.ConversationCapability.Name, Name: plugins.ConversationMessageEvent, Data: data}
}

func assertConversationDenied(t *testing.T, err error) {
	t.Helper()
	var wire *plugins.WireError
	if !errors.As(err, &wire) || wire.Category != plugins.ErrorPermissionDenied {
		t.Fatalf("expected permission_denied, got %v", err)
	}
}
