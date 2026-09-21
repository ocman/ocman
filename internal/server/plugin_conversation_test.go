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
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/plugins"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

const conversationTestThread = "slackC1:1700000000.000100"

func conversationPluginDescription() plugin.Description {
	return plugin.Description{
		ID: "org.example.chatops", Name: "Chat Ops", Version: "1", Protocol: plugin.Version{Major: 1},
		Scope: plugin.ScopeOwner, MaxConcurrency: 2,
		Capabilities:    []plugin.Capability{plugin.ConversationCapability},
		RequestedGrants: []string{plugin.ConversationSessionGrant},
		Settings: []plugin.Setting{
			{Key: plugin.ConversationProjectSetting, Label: "Project", Type: "string", Required: true},
			{Key: "trigger", Label: "Trigger text", Type: "string"},
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
			Trigger string `json:"trigger"`
			Token   string `json:"token"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(line), &cfg)
		handler = plugin.ConversationHandler(func(_ context.Context, r plugin.ConversationReply) error {
			out, _ := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = fmt.Fprintf(out, "reply\t%s\t%s\n", r.ThreadID, strings.ReplaceAll(r.Text, "\n", "\\n"))
			_ = out.Close()
			return nil
		})
		events = make(chan plugin.Event, 4)
		if cfg.Trigger != "" {
			message, err := plugin.NewConversationMessage(plugin.ConversationMessage{ThreadID: conversationTestThread, Text: cfg.Trigger})
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
func (*conversationTestHost) EnsureProjectOpencode(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:1234", RepoRoot: req.ProjectDir}, nil
}

type conversationFixture struct {
	s       *Server
	mux     *http.ServeMux
	cookie  *http.Cookie
	record  string
	project string

	mu       sync.Mutex
	prompts  []string
	sessions int
}

func newConversationFixture(t *testing.T) *conversationFixture {
	t.Helper()
	pluginDir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", pluginDir)
	stateDB, err := state.Open(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })

	f := &conversationFixture{
		record:  filepath.Join(t.TempDir(), "replies"),
		project: mustEvalSymlinks(t, t.TempDir()),
	}
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{
		id:       "opencode",
		sessions: []db.Session{{ID: "ses-chat"}},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.sessions++
			if req.Directory != f.project {
				t.Errorf("session created outside the approved project: %q", req.Directory)
			}
			return &platforms.CreateSessionResponse{ID: "ses-chat"}, nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.prompts = append(f.prompts, req.Message)
			return nil
		},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session: &db.Session{ID: "ses-chat", Directory: f.project},
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
		stateDB: stateDB, pluginCtx: context.Background(), auth: newTestAuth(t, "password"),
		registry: registry, hostRouter: hostsvc.NewRouter(&conversationTestHost{}),
	}
	f.s.sessions = sessionsvc.New(registry, sessionsvc.Hooks{})
	t.Cleanup(f.s.stopPluginProcesses)

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestConversationPluginHelper$ -- %q \"$@\"\n", exe, f.record)
	if err := os.WriteFile(filepath.Join(pluginDir, "ocman-plugin-chatops"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
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
	configuration, err := json.Marshal(pluginManagementInput{
		Values:  map[string]json.RawMessage{"project": mustJSON(t, project), "trigger": mustJSON(t, "ship it")},
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
	enable, err := json.Marshal(pluginManagementInput{Approval: p.Approval, Grants: &grants})
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
	// The control character in the assistant text is stripped, not dropped.
	want := "reply\t" + conversationTestThread + "\tfirst half\\n\\nsecondhalf\n"
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
	event := conversationMessageEvent(t, plugins.ConversationMessage{ThreadID: conversationTestThread, Text: "again"})
	err := f.s.conversations().Deliver(t.Context(), conversationPluginDescription().ID, event)
	assertConversationDenied(t, err)
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 1 {
		t.Fatalf("ungranted message reached a session: %v", prompts)
	}
	// A revoked grant also blocks the outbound reply.
	assertConversationDenied(t, f.s.conversations().Reply(t.Context(), conversationPluginDescription().ID, "op-1",
		plugins.ConversationReply{ThreadID: conversationTestThread, Text: "leak"}))
}

func TestConversationPluginDeniedForUnapprovedProject(t *testing.T) {
	f := newConversationFixture(t)
	missing := filepath.Join(f.project, "not-a-project")
	f.install(t, missing, []string{plugins.ConversationSessionGrant})
	id := conversationPluginDescription().ID

	// A project the host cannot resolve never produces a session.
	err := f.s.conversations().Deliver(t.Context(), id, conversationMessageEvent(t, plugins.ConversationMessage{ThreadID: conversationTestThread, Text: "go"}))
	assertConversationDenied(t, err)

	// Nor does a plugin naming a project other than its configured one.
	claim := plugins.ConversationMessage{ThreadID: conversationTestThread, Text: "go", Project: f.project}
	assertConversationDenied(t, f.s.conversations().Deliver(t.Context(), id, conversationMessageEvent(t, claim)))
	if prompts := f.awaitPromptsNoGrowth(t); len(prompts) != 0 {
		t.Fatalf("unapproved project started a session: %v", prompts)
	}
}

func TestConversationPluginDisabledIsUnavailable(t *testing.T) {
	f := newConversationFixture(t)
	f.call(t, "POST", "/rescan", `{}`, 200)
	err := f.s.conversations().Deliver(t.Context(), conversationPluginDescription().ID,
		conversationMessageEvent(t, plugins.ConversationMessage{ThreadID: conversationTestThread, Text: "go"}))
	if err == nil {
		t.Fatal("a disabled plugin reached session orchestration")
	}
}

func (f *conversationFixture) awaitPromptsNoGrowth(t *testing.T) []string {
	t.Helper()
	time.Sleep(200 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.prompts...)
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

func TestLatestAssistantText(t *testing.T) {
	messages := []db.Message{
		{ID: "m1", TimeCreated: 100, Data: json.RawMessage(`{"role":"user"}`)},
		{ID: "m3", TimeCreated: 300, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "m2", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
		{ID: "broken", TimeCreated: 400, Data: json.RawMessage(`not json`)},
	}
	// Stored parts carry timestamps and may arrive unordered.
	stored := []db.Part{
		{ID: "b", MessageID: "m3", TimeCreated: 320, Data: json.RawMessage(`{"type":"text","text":"world\n"}`)},
		{ID: "a", MessageID: "m3", TimeCreated: 310, Data: json.RawMessage(`{"type":"text","text":"hello"}`)},
		{ID: "c", MessageID: "m3", TimeCreated: 330, Data: json.RawMessage(`{"type":"tool","text":"ignored"}`)},
		{ID: "d", MessageID: "m3", TimeCreated: 340, Data: json.RawMessage(`{"type":"text","text":"   "}`)},
		{ID: "e", MessageID: "m2", TimeCreated: 210, Data: json.RawMessage(`{"type":"text","text":"older"}`)},
	}
	id, text := latestAssistantText(messages, stored)
	if id != "m3" || text != "hello\n\nworld" {
		t.Fatalf("%q %q", id, text)
	}
	// Live parts have no timestamps: arrival order must survive, not ID order.
	live := []db.Part{
		{ID: "zz", MessageID: "m3", Data: json.RawMessage(`{"type":"text","text":"hello"}`)},
		{ID: "aa", MessageID: "m3", Data: json.RawMessage(`{"type":"text","text":"world"}`)},
	}
	if _, text := latestAssistantText(messages, live); text != "hello\n\nworld" {
		t.Fatalf("live part order not preserved: %q", text)
	}
	if id, text := latestAssistantText(messages[:1], stored); id != "" || text != "" {
		t.Fatalf("no assistant message should yield nothing: %q %q", id, text)
	}
	long := []db.Part{{ID: "x", MessageID: "m3", Data: mustPartText(t, strings.Repeat("y", plugins.ConversationMaxTextBytes+10))}}
	if _, text := latestAssistantText(messages, long); len(text) > plugins.ConversationMaxTextBytes {
		t.Fatalf("oversized reply not truncated: %d", len(text))
	}
}

// Assistant answers can contain terminal escapes; the whole reply must not be
// dropped because conversation.v1 forbids them on the wire.
func TestConversationReplyTextSanitizes(t *testing.T) {
	messages := []db.Message{{ID: "m1", TimeCreated: 1, Data: json.RawMessage(`{"role":"assistant"}`)}}
	parts := []db.Part{{ID: "p", MessageID: "m1", Data: mustPartText(t, "before\x1b[31m\x07after\ttab")}}
	_, text := latestAssistantText(messages, parts)
	if text != "before[31mafter\ttab" {
		t.Fatalf("sanitized text %q", text)
	}
	if (plugins.ConversationReply{ThreadID: conversationTestThread, Text: text}).Validate() != nil {
		t.Fatal("sanitized text still fails the wire contract")
	}
	trailing := []db.Part{{ID: "p", MessageID: "m1", Data: mustPartText(t, "ok\x1b")}}
	if _, text := latestAssistantText(messages, trailing); text != "ok" {
		t.Fatalf("trailing escape not stripped: %q", text)
	}
}

func mustPartText(t *testing.T, text string) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(map[string]string{"type": "text", "text": text})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
