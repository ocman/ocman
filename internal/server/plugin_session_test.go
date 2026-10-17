package server

import (
	"github.com/NoUseFreak/ocman/internal/pluginsmod"

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
	"github.com/NoUseFreak/ocman/internal/remote"
	"github.com/NoUseFreak/ocman/internal/sessionsvc"
	"github.com/NoUseFreak/ocman/internal/state"
	plugin "github.com/NoUseFreak/ocman/sdk/plugin"
)

func sessionPluginDescription() plugin.Description {
	return plugin.Description{
		ID: "org.example.sessionops", Name: "Session Ops", Version: "1", Protocol: plugin.Version{Major: 1},
		Scope: plugin.ScopeOwner, MaxConcurrency: 2,
		Capabilities:    []plugin.Capability{plugin.SessionCapability},
		RequestedGrants: []string{plugin.SessionRunGrant},
		Settings: []plugin.Setting{
			{Key: plugin.SessionProjectSetting, Label: "Project", Type: "string", Required: true},
			{Key: "prompt", Label: "Prompt to fire on start", Type: "string"},
			{Key: "agent", Label: "Agent", Type: "string"},
			{Key: "model", Label: "Model", Type: "string"},
			{Key: "profile", Label: "Permission profile", Type: "string"},
			{Key: "subpath", Label: "Subpath", Type: "string"},
			{Key: "completionMode", Label: "Completion test mode", Type: "string"},
		},
	}
}

// TestPluginSessionHelper is a real plugin executable, re-exec'd by a shell
// shim. On serve it emits one run request (when configured with a prompt) and
// records every completed turn the host delivers back, with its operation id,
// so a test can see both that a delivery landed and that its identity is
// stable.
func TestPluginSessionHelper(t *testing.T) {
	if len(os.Args) != 5 || os.Args[2] != "--" {
		return
	}
	record, mode := os.Args[3], plugin.Mode(os.Args[4])
	d := sessionPluginDescription()
	var handler plugin.Handler
	var requests chan plugin.Request
	if mode == plugin.ModeServe {
		f := os.NewFile(3, "config")
		line, _ := bufio.NewReader(io.LimitReader(f, plugin.MaxMessageBytes)).ReadBytes('\n')
		_ = f.Close()
		var cfg struct {
			Prompt         string `json:"prompt"`
			Agent          string `json:"agent"`
			Model          string `json:"model"`
			Profile        string `json:"profile"`
			Subpath        string `json:"subpath"`
			CompletionMode string `json:"completionMode"`
		}
		_ = json.Unmarshal(bytes.TrimSpace(line), &cfg)
		handler = plugin.SessionHandler(func(_ context.Context, operationID string, c plugin.SessionCompleted) error {
			calls, _ := os.OpenFile(record+".calls", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = fmt.Fprintln(calls, operationID)
			_ = calls.Close()
			// A durable consumer receipt keeps a replay from applying the effect twice.
			marker, err := os.OpenFile(record+"."+operationID, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if errors.Is(err, os.ErrExist) {
				return nil
			}
			if err != nil {
				return err
			}
			_ = marker.Close()
			out, _ := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			_, _ = fmt.Fprintf(out, "completed\t%s\t%s\t%s\n", c.SessionID, operationID, strings.ReplaceAll(c.Text, "\n", "\\n"))
			_ = out.Sync()
			_ = out.Close()
			if cfg.CompletionMode == "crash-once" {
				os.Exit(1)
			}
			return nil
		})
		if cfg.CompletionMode == "invalid-ack" {
			handler = func(context.Context, plugin.Call, func(json.RawMessage) error) (json.RawMessage, error) {
				return json.RawMessage(`{"extra":true}`), nil
			}
		}
		requests = make(chan plugin.Request, 1)
		if cfg.Prompt != "" {
			call, err := plugin.NewSessionRun("caller-run-1", plugin.SessionRunParams{
				Prompt: cfg.Prompt, Agent: cfg.Agent, Model: cfg.Model, PermissionProfile: cfg.Profile, Subpath: cfg.Subpath,
			})
			if err == nil {
				replies := make(chan plugin.Result, 1)
				requests <- plugin.Request{Call: call, Replies: replies}
				go func() {
					result := <-replies
					out, _ := os.OpenFile(record, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
					defer out.Close()
					if result.Error != nil {
						_, _ = fmt.Fprintf(out, "run\t%d\terror\t%s\n", result.ID, result.Error.Category)
					} else {
						_, _ = fmt.Fprintf(out, "run\t%d\t%s\n", result.ID, result.Value)
					}
				}()
			}
		}
	}
	_ = plugin.RunWithRequests(context.Background(), mode, os.Getenv("OCMAN_PLUGIN_TOKEN"), d, os.Stdin, os.Stdout, handler, nil, requests)
	os.Exit(0)
}

type sessionFixture struct {
	s         *Server
	mux       *http.ServeMux
	cookie    *http.Cookie
	record    string
	project   string
	pluginDir string
	statePath string

	mu          sync.Mutex
	created     []platforms.CreateSessionRequest
	rules       []platforms.SetPermissionRulesRequest
	prompts     []platforms.SendMessageRequest
	sessions    int
	createErr   error
	setRulesErr error
}

func newSessionFixture(t *testing.T) *sessionFixture {
	t.Helper()
	pluginDir := t.TempDir()
	t.Setenv("OCMAN_PLUGIN_DIR", pluginDir)
	statePath := testStatePath(t)
	stateDB, err := state.Open(statePath, ModuleSchemas()...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateDB.Close() })

	f := &sessionFixture{
		record:    filepath.Join(t.TempDir(), "completions"),
		project:   mustEvalSymlinks(t, t.TempDir()),
		pluginDir: pluginDir,
		statePath: statePath,
	}
	registry := platforms.NewRegistry()
	registry.Register(&fakePlatform{
		id:       "opencode",
		sessions: []db.Session{{ID: "ses-run-1"}, {ID: "ses-run-2"}},
		createSessionFn: func(req platforms.CreateSessionRequest) (*platforms.CreateSessionResponse, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.createErr != nil {
				return nil, f.createErr
			}
			f.sessions++
			f.created = append(f.created, req)
			return &platforms.CreateSessionResponse{ID: fmt.Sprintf("ses-run-%d", f.sessions)}, nil
		},
		setPermissionRulesFn: func(req platforms.SetPermissionRulesRequest) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.setRulesErr != nil {
				return f.setRulesErr
			}
			f.rules = append(f.rules, req)
			return nil
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.prompts = append(f.prompts, req)
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session: &db.Session{ID: id, Directory: f.project, Status: db.StatusDone},
				Messages: []db.Message{
					{ID: "m1", TimeCreated: 100, Data: json.RawMessage(`{"role":"user"}`)},
					{ID: "m2", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant"}`)},
				},
				Parts: []db.Part{
					{ID: "p0", MessageID: "m1", Data: json.RawMessage(`{"type":"text","text":"the prompt"}`)},
					// A control character exercises the reply sanitizer; the reasoning
					// part must never reach the plugin.
					{ID: "p1", MessageID: "m2", Data: json.RawMessage(`{"type":"text","text":"ans\bwer"}`)},
					{ID: "p2", MessageID: "m2", Data: json.RawMessage(`{"type":"reasoning","text":"private"}`)},
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
	script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestPluginSessionHelper$ -- %q \"$@\"\n", exe, f.record)
	if err := os.WriteFile(filepath.Join(pluginDir, "ocman-plugin-sessionops"), []byte(script), 0700); err != nil {
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

func (f *sessionFixture) call(t *testing.T, method, path, body string, status int) string {
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
func (f *sessionFixture) install(t *testing.T, values map[string]string, grants []string) {
	t.Helper()
	id := sessionPluginDescription().ID
	f.call(t, "POST", "/rescan", `{}`, 200)
	raw := make(map[string]json.RawMessage, len(values))
	for k, v := range values {
		raw[k] = mustJSON(t, v)
	}
	configuration, err := json.Marshal(remote.PluginInput{Values: raw})
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

func (f *sessionFixture) awaitPrompts(t *testing.T, want int) []platforms.SendMessageRequest {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		f.mu.Lock()
		prompts := append([]platforms.SendMessageRequest(nil), f.prompts...)
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

func (f *sessionFixture) awaitRecord(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if data, err := os.ReadFile(f.record); err == nil && strings.Contains(string(data), want) {
			return
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(f.record)
			t.Fatalf("expected record to contain %q, saw %q", want, data)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (f *sessionFixture) recordText() string {
	data, _ := os.ReadFile(f.record)
	return string(data)
}

func (f *sessionFixture) createRequests() []platforms.CreateSessionRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]platforms.CreateSessionRequest(nil), f.created...)
}

func (f *sessionFixture) ruleRequests() []platforms.SetPermissionRulesRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]platforms.SetPermissionRulesRequest(nil), f.rules...)
}

// TestPluginSessionRunAndCompletion is the acceptance walkthrough: a
// conforming plugin runs against the approved project (with a contained
// subpath and a named profile), receives its sessionId before completion,
// and the turn text reaches the plugin exactly once even when the idle edge
// repeats.
func TestPluginSessionRunAndCompletion(t *testing.T) {
	f := newSessionFixture(t)
	ctx := t.Context()
	rules := []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "deny"}}
	rulesJSON, err := json.Marshal(rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := pluginsmod.NewStore(f.s.stateDB).CreatePermissionProfile(ctx, pluginsmod.PermissionProfile{ID: "p1", Name: "read-only", RulesJSON: string(rulesJSON)}); err != nil {
		t.Fatal(err)
	}
	// The subpath must be an existing directory inside the approved project.
	if err := os.MkdirAll(filepath.Join(f.project, "packages/app"), 0700); err != nil {
		t.Fatal(err)
	}
	f.install(t, map[string]string{
		"project": f.project, "prompt": "build it", "subpath": "packages/app",
		"profile": "read-only", "agent": "plan", "model": "provider/model",
	}, []string{plugin.SessionRunGrant})

	prompts := f.awaitPrompts(t, 1)
	f.awaitRecord(t, "run\t1\t{\"sessionId\":\"ses-run-1\"}\n")
	if strings.Contains(f.recordText(), "completed") {
		t.Fatal("completion preceded immediate run result")
	}
	if prompts[0].Message != "build it" || prompts[0].Agent != "plan" || prompts[0].Model != "provider/model" {
		t.Fatalf("queued prompt %+v", prompts[0])
	}
	created := f.createRequests()
	if len(created) != 1 {
		t.Fatalf("created %v", created)
	}
	// The directory is the approved project joined with a contained subpath;
	// a named profile pins its exact rules at CreateConfigured time; the port
	// comes from the discovered project instance.
	if created[0].Directory != filepath.Join(f.project, "packages/app") {
		t.Fatalf("directory %q", created[0].Directory)
	}
	if created[0].Title != "plugin:"+sessionPluginDescription().ID+":caller-run-1" {
		t.Fatalf("title %q", created[0].Title)
	}
	if created[0].Port != "1234" {
		t.Fatalf("port %q", created[0].Port)
	}
	requests := f.ruleRequests()
	if len(requests) != 1 || requests[0].SessionID != "ses-run-1" || len(requests[0].Rules) != 1 || requests[0].Rules[0] != rules[0] {
		t.Fatalf("profile rules not pinned: %+v", requests)
	}
	if pluginID, linked, err := pluginsmod.NewStore(f.s.stateDB).GetPluginSessionPlugin(ctx, "opencode", "ses-run-1"); err != nil || !linked || pluginID != sessionPluginDescription().ID {
		t.Fatalf("session not linked to its plugin: %q %v %v", pluginID, linked, err)
	}

	// The completed turn reaches the plugin exactly once, with the sessionId
	// the run started and the newest assistant text only.
	f.s.replyToSession(ctx, "opencode", "ses-run-1")
	f.awaitRecord(t, "completed\tses-run-1\tsess-out:1\tanswer\n")
	// A repeated idle edge for the same turn (and a reconciliation pass over
	// the durable mapping) appends nothing.
	before := f.recordText()
	f.s.replyToSession(ctx, "opencode", "ses-run-1")
	f.s.pluginsModule().ReconcileSessionCompletions(ctx)
	f.s.replyToSession(ctx, "opencode", "ses-run-1")
	if got := f.recordText(); got != before {
		t.Fatalf("repeated idle edge re-delivered: %q", got)
	}
	if has, err := pluginsmod.NewStore(f.s.stateDB).HasPluginSessionCompletion(ctx, sessionPluginDescription().ID, "ses-run-1:m2"); err != nil || !has {
		t.Fatalf("durable receipt missing: %v %v", has, err)
	}
}

// TestPluginSessionRunDenials checks the fail-closed boundaries: ungranted
// plugins, unapproved subpaths, missing profiles and an unset project.
func TestPluginSessionRunDenials(t *testing.T) {
	f := newSessionFixture(t)
	id := sessionPluginDescription().ID
	ctx := t.Context()
	run := func(params plugins.SessionRunParams) error {
		data, err := json.Marshal(params)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.s.pluginsModule().SessionBroker().Run(ctx, id, plugins.Call{OperationID: "caller-run-1", Capability: plugins.SessionCapability.Name, Version: plugins.SessionCapability.Version, Method: plugins.SessionRunMethod, Params: data})
		return err
	}
	// No registration at all fails closed.
	if err := run(plugins.SessionRunParams{Prompt: "go"}); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("unregistered plugin: %v", err)
	}

	f.install(t, map[string]string{"project": f.project}, []string{plugin.SessionRunGrant})
	// Revoking the grant stops runs without disabling the plugin.
	empty := []string{}
	grantsBody, err := json.Marshal(remote.PluginInput{Grants: &empty})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+id+"/grants", string(grantsBody), 200)
	if err := run(plugins.SessionRunParams{Prompt: "go"}); !isWire(err, plugins.ErrorPermissionDenied) {
		t.Fatalf("revoked grant still runs: %v", err)
	}
	enable, err := json.Marshal(remote.PluginInput{Grants: &[]string{plugin.SessionRunGrant}})
	if err != nil {
		t.Fatal(err)
	}
	f.call(t, "POST", "/"+id+"/grants", string(enable), 200)
	for _, bad := range []string{"../escape", "/etc", "..", "a/../../b", "missing-dir"} {
		if err := run(plugins.SessionRunParams{Prompt: "go", Subpath: bad}); !isWire(err, plugins.ErrorPermissionDenied) {
			t.Fatalf("subpath %q accepted: %v", bad, err)
		}
	}
	if err := run(plugins.SessionRunParams{Prompt: "go", PermissionProfile: "ghost"}); !isWire(err, plugins.ErrorNotFound) {
		t.Fatalf("unknown profile: %v", err)
	}
	if err := run(plugins.SessionRunParams{}); err == nil {
		t.Fatal("empty prompt accepted")
	}
	if got := len(f.createRequests()); got != 0 {
		t.Fatalf("denied runs created %d sessions", got)
	}
	if len(f.awaitPromptsNoWait()) != 0 {
		t.Fatal("denied runs queued prompts")
	}
}

func (f *sessionFixture) awaitPromptsNoWait() []platforms.SendMessageRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]platforms.SendMessageRequest(nil), f.prompts...)
}

func isWire(err error, want plugins.ErrorCategory) bool {
	var wire *plugins.WireError
	return errors.As(err, &wire) && wire.Category == want
}

// TestPluginSessionRunPausesAtBacklogCap verifies that run admission pauses
// (and fails closed) while a plugin's completion backlog sits at its cap,
// rather than accepting work whose turns could not be delivered.
func TestPluginSessionRunPausesAtBacklogCap(t *testing.T) {
	f := newSessionFixture(t)
	id := sessionPluginDescription().ID
	ctx := t.Context()
	f.install(t, map[string]string{"project": f.project}, []string{plugin.SessionRunGrant})
	for i := range state.PluginConversationOutboxMaxRows {
		if _, err := pluginsmod.NewStore(f.s.stateDB).AppendPluginSessionCompletion(ctx, id, "opencode", "ses-full", fmt.Sprintf("op-%d", i), "x"); err != nil {
			t.Fatal(err)
		}
	}
	data, err := json.Marshal(plugins.SessionRunParams{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.pluginsModule().SessionBroker().Run(ctx, id, plugins.Call{OperationID: "caller-run-1", Capability: plugins.SessionCapability.Name, Version: plugins.SessionCapability.Version, Method: plugins.SessionRunMethod, Params: data}); !errors.Is(err, plugins.ErrUnavailable) {
		t.Fatalf("run admitted at backlog cap: %v", err)
	}
	if got := len(f.createRequests()); got != 0 {
		t.Fatalf("paused run created %d sessions", got)
	}
}
