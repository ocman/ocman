package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providerprotocol"
)

type fakeReply struct {
	status int
	body   any
}
type fakeCall struct {
	method, path, query string
	body                json.RawMessage
}
type fakeProvider struct {
	socket  string
	hello   providerprotocol.Hello
	mu      sync.Mutex
	calls   []fakeCall
	replies map[string]fakeReply
	events  func(http.ResponseWriter, *http.Request)
	hook    func(http.ResponseWriter, *http.Request) bool
}

func allCapabilities() providerprotocol.Capabilities {
	return providerprotocol.Capabilities{Capabilities: platforms.Capabilities{
		Composer: true, RespondPermission: true, RespondQuestion: true, Abort: true,
		Compact: true, Fork: true, Move: true, Events: true, AgentCatalog: true,
		ModelCatalog: true, SlashCommands: true, ShellExec: true, FileChanges: true,
		SessionInfo: true, AutoApprove: true, PermissionRules: true,
	}, Rename: true, NativeQueue: true, Account: true}
}

func newFake(t *testing.T, caps providerprotocol.Capabilities) *fakeProvider {
	t.Helper()
	// Short paths avoid Darwin's unix socket path length limit.
	dir, err := os.MkdirTemp("", "p-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fakeProvider{socket: filepath.Join(dir, "s"), hello: providerprotocol.Hello{
		ProtocolVersion: providerprotocol.CurrentVersion, PlatformID: "fake", CLIVersion: "test-only", Capabilities: caps,
	}, replies: make(map[string]fakeReply)}
	listener, err := net.Listen("unix", f.socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.calls = append(f.calls, fakeCall{r.Method, r.URL.Path, r.URL.RawQuery, body})
		hello, eventHandler, reply := f.hello, f.events, f.replies[r.Method+" "+r.URL.Path]
		hook := f.hook
		f.mu.Unlock()
		if hook != nil && hook(w, r) {
			return
		}
		if r.URL.Path == "/hello" {
			reply = fakeReply{200, hello}
		}
		if r.URL.Path == "/events" && eventHandler != nil {
			eventHandler(w, r)
			return
		}
		if reply.status == 0 {
			reply.status = 204
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(reply.status)
		if reply.body != nil {
			_ = json.NewEncoder(w).Encode(reply.body)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return f
}
func (f *fakeProvider) reply(method, path string, body any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[method+" "+path] = fakeReply{200, body}
}
func (f *fakeProvider) adapter(t *testing.T) *Adapter {
	t.Helper()
	a, err := New(t.Context(), f.socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return a
}
func requireNoError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionContract(t *testing.T) {
	f := newFake(t, allCapabilities())
	s := db.Session{ID: "s", Directory: "/work tree", TimeUpdated: 10}
	f.reply("GET", "/sessions", []db.Session{s, {ID: "recent", TimeUpdated: 30}})
	f.reply("GET", "/sessions/s", platforms.SessionDetail{Session: &s,
		SessionTree: []db.Session{{ID: "child"}}, TotalMessages: 100,
		Messages: []db.Message{{ID: "m", SessionID: "s"}}, Parts: []db.Part{{ID: "p", MessageID: "m"}},
	})
	f.reply("GET", "/sessions/s/lifecycle", platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m"})
	a := f.adapter(t)
	if a.ID() != "fake" || a.DisplayName() != "fake" || !a.Available(t.Context()) || a.Hello().CLIVersion != "test-only" {
		t.Fatal("handshake")
	}
	// Provider rules are escaped (ExactPattern) whatever the hello says.
	if !a.Capabilities().ExactPatterns {
		t.Fatal("provider capabilities must report ExactPatterns")
	}
	if a.Knows("s") {
		t.Fatal("ownership before observation")
	}
	sessions, err := a.Sessions(t.Context(), "/work tree", 7)
	requireNoError(t, err)
	if len(sessions) != 2 || sessions[0].Platform != "fake" || !a.Knows("s") {
		t.Fatal(sessions)
	}
	detail, err := a.RuntimeDetail(t.Context(), "s", 20, 40)
	requireNoError(t, err)
	if detail.TotalMessages != 100 || len(detail.Parts) != 1 || detail.SessionTree[0].Platform != "fake" || !a.Knows("child") {
		t.Fatal(detail)
	}
	lifecycle, err := a.SessionLifecycle(t.Context(), "s")
	requireNoError(t, err)
	if lifecycle.Status != db.StatusBusy || lifecycle.LatestMessageID != "m" {
		t.Fatal(lifecycle)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls[2].query != "directory=%2Fwork+tree&since=7" || f.calls[3].query != "limit=20&offset=40" {
		t.Fatal(f.calls)
	}
}

func TestRuntimeSessionsAndHooks(t *testing.T) {
	caps := allCapabilities()
	caps.Hooks = true
	f := newFake(t, caps)
	f.reply("GET", "/sessions", []db.Session{{ID: "helper"}})
	a := f.adapter(t)
	sessions, err := a.RuntimeSessions(t.Context())
	requireNoError(t, err)
	if len(sessions) != 1 || !a.Knows("helper") {
		t.Fatalf("runtime sessions = %+v", sessions)
	}
	requireNoError(t, a.Hook(t.Context(), providerprotocol.HookEvent{Event: "Stop", SessionID: "outside"}))
	f.mu.Lock()
	defer f.mu.Unlock()
	last := f.calls[len(f.calls)-1]
	if f.calls[len(f.calls)-2].query != "runtime=1" || last.method != "POST" || last.path != "/hooks" {
		t.Fatal(f.calls)
	}
}

func TestMutationContract(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.reply("POST", "/sessions", platforms.CreateSessionResponse{ID: "created"})
	f.reply("POST", "/sessions/s/fork", platforms.CreateSessionResponse{ID: "forked"})
	a := f.adapter(t)
	created, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: "/repo", Title: "new"})
	requireNoError(t, err)
	if created.ID != "created" || !a.Knows("created") {
		t.Fatal(created)
	}
	forked, err := a.ForkSession(t.Context(), platforms.ForkSessionRequest{SessionID: "s", MessageID: "m"})
	requireNoError(t, err)
	if forked.ID != "forked" || !a.Knows("forked") {
		t.Fatal(forked)
	}
	send := platforms.SendMessageRequest{SessionID: "s", Message: "hello", Images: []platforms.ImageAttachment{{URL: "data:image/png;base64,AA==", Mime: "image/png"}}, Model: "model", Agent: "build", Reasoning: "high"}
	queued := send
	queued.Delivery = "queue"
	tests := []struct {
		path string
		body any
		run  func() error
	}{
		{"/sessions/s/send", send, func() error { return a.SendMessage(t.Context(), send) }},
		{"/sessions/s/hold", queued, func() error { return a.SendMessage(t.Context(), queued) }},
		{"/sessions/s/abort", platforms.AbortRequest{SessionID: "s"}, func() error { return a.Abort(t.Context(), platforms.AbortRequest{SessionID: "s"}) }},
		{"/sessions/s/rename", platforms.RenameSessionRequest{SessionID: "s", Title: "renamed"}, func() error {
			return a.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: "s", Title: "renamed"})
		}},
		{"/sessions/s/compact", platforms.CompactRequest{SessionID: "s", ModelID: "m"}, func() error { return a.Compact(t.Context(), platforms.CompactRequest{SessionID: "s", ModelID: "m"}) }},
		{"/sessions/s/command", platforms.ExecuteCommandRequest{SessionID: "s", Command: "skill", Arguments: "args"}, func() error {
			return a.ExecuteCommand(t.Context(), platforms.ExecuteCommandRequest{SessionID: "s", Command: "skill", Arguments: "args"})
		}},
		{"/sessions/s/shell", platforms.RunShellRequest{SessionID: "s", Command: "pwd"}, func() error {
			return a.RunShell(t.Context(), platforms.RunShellRequest{SessionID: "s", Command: "pwd"})
		}},
		{"/permissions/p/reply", platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "always"}, func() error {
			return a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p", Reply: "always"})
		}},
		{"/questions/q/reply", platforms.RespondQuestionRequest{SessionID: "s", RequestID: "q", Answers: [][]string{{"yes"}}}, func() error {
			return a.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{SessionID: "s", RequestID: "q", Answers: [][]string{{"yes"}}})
		}},
		{"/questions/q/reject", platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q"}, func() error {
			return a.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q"})
		}},
		{"/sessions/s/rules", platforms.SetPermissionRulesRequest{SessionID: "s", Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}}}, func() error {
			return a.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{SessionID: "s", Rules: []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}}})
		}},
		{"/sessions/s/move", platforms.MoveSessionRequest{SessionID: "s", Directory: "/other"}, func() error {
			return a.MoveSession(t.Context(), platforms.MoveSessionRequest{SessionID: "s", Directory: "/other"})
		}},
		{"/sessions/s/held/h/cancel", platforms.CancelNativeQueuedRequest{SessionID: "s", ID: "h"}, func() error {
			return a.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{SessionID: "s", ID: "h"})
		}},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			requireNoError(t, tt.run())
			f.mu.Lock()
			call := f.calls[len(f.calls)-1]
			f.mu.Unlock()
			expected, _ := json.Marshal(tt.body)
			if call.method != "POST" || call.path != tt.path || !bytes.Equal(call.body, expected) {
				t.Fatalf("got %+v, expected %s", call, expected)
			}
		})
	}
}

func TestCatalogAndOptionalReadContract(t *testing.T) {
	f := newFake(t, allCapabilities())
	s := db.Session{ID: "s", Directory: "/repo"}
	f.reply("GET", "/sessions/s", platforms.SessionDetail{Session: &s})
	catalog := platforms.DirectoryCatalog{Agents: []platforms.AgentCatalogEntry{{Name: "build"}}, Commands: []platforms.SlashCommandEntry{{Name: "skill"}}, Models: &platforms.SessionModelsResponse{Models: []platforms.SessionModel{{Model: "model"}}}, LiveConnection: true}
	f.reply("GET", "/catalog", catalog)
	f.reply("GET", "/sessions/s/rules", []platforms.PermissionRule{{Permission: "bash", Pattern: "*", Action: "ask"}})
	f.reply("GET", "/sessions/s/held", []platforms.NativeQueuedMessage{{ID: "h", Text: "followup"}})
	f.reply("GET", "/account", providerprotocol.Account{SubscriptionType: "max", RateLimits: []providerprotocol.RateLimit{{Name: "weekly", Utilization: 0.5, ResetsAt: 100}}})
	a := f.adapter(t)
	direct, err := a.LiveDirectoryCatalog(t.Context(), platforms.DirectoryCatalogRequest{Directory: "/repo"})
	requireNoError(t, err)
	if !direct.LiveConnection || direct.Agents[0].Name != "build" || direct.Commands[0].Name != "skill" || direct.Models.Models[0].Model != "model" {
		t.Fatal(direct)
	}
	rules, err := a.PermissionRules(t.Context(), "s")
	requireNoError(t, err)
	if rules[0].Action != "ask" {
		t.Fatal(rules)
	}
	held, err := a.NativeQueued(t.Context(), "s")
	requireNoError(t, err)
	if held[0].Text != "followup" {
		t.Fatal(held)
	}
	account, err := a.Account(t.Context())
	requireNoError(t, err)
	if account.SubscriptionType != "max" || account.RateLimits[0].Utilization != .5 {
		t.Fatal(account)
	}
}

func TestCapabilitiesGateEveryOptionalMethod(t *testing.T) {
	f := newFake(t, providerprotocol.Capabilities{})
	a := f.adapter(t)
	tests := []func() error{
		func() error { return a.SendMessage(t.Context(), platforms.SendMessageRequest{}) },
		func() error { return a.SendMessage(t.Context(), platforms.SendMessageRequest{Delivery: "queue"}) },
		func() error { return a.ExecuteCommand(t.Context(), platforms.ExecuteCommandRequest{}) },
		func() error { return a.RunShell(t.Context(), platforms.RunShellRequest{}) },
		func() error { return a.Abort(t.Context(), platforms.AbortRequest{}) },
		func() error { return a.Compact(t.Context(), platforms.CompactRequest{}) },
		func() error { return a.RenameSession(t.Context(), platforms.RenameSessionRequest{}) },
		func() error { return a.MoveSession(t.Context(), platforms.MoveSessionRequest{}) },
		func() error { return a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{}) },
		func() error { return a.RespondQuestion(t.Context(), platforms.RespondQuestionRequest{}) },
		func() error { return a.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{}) },
		func() error { return a.SetPermissionRules(t.Context(), platforms.SetPermissionRulesRequest{}) },
		func() error { _, err := a.PermissionRules(t.Context(), "s"); return err },
		func() error { _, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{}); return err },
		func() error { _, err := a.ForkSession(t.Context(), platforms.ForkSessionRequest{}); return err },
		func() error { _, err := a.NativeQueued(t.Context(), "s"); return err },
		func() error { return a.CancelNativeQueued(t.Context(), platforms.CancelNativeQueuedRequest{}) },
		func() error { _, err := a.Account(t.Context()); return err },
		func() error { _, err := a.ListPermissions(t.Context(), "s"); return err },
		func() error { _, err := a.ListQuestions(t.Context(), "s"); return err },
		func() error { _, err := a.RefreshPermissions(t.Context(), "s"); return err },
		func() error { return a.ProxyEvents(t.Context(), "s", io.Discard, nil) },
		func() error { return a.WatchEvents(t.Context()) },
	}
	for i, run := range tests {
		if err := run(); !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("method %d: %v", i, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) != 1 {
		t.Fatal("unsupported method contacted provider", f.calls)
	}
}

func TestVersionsAndHandshakeErrors(t *testing.T) {
	for _, version := range []int{-1, 0, 1, 2, 3, 100} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			f := newFake(t, allCapabilities())
			f.hello.ProtocolVersion = version
			a, err := New(t.Context(), f.socket)
			if version == providerprotocol.PreviousVersion || version == providerprotocol.CurrentVersion {
				requireNoError(t, err)
				a.Close()
				return
			}
			var mismatch *providerprotocol.VersionError
			if !errors.As(err, &mismatch) || mismatch.Version != version || !strings.Contains(err.Error(), "supported") {
				t.Fatal(err)
			}
		})
	}
	f := newFake(t, allCapabilities())
	f.hello.PlatformID = ""
	if _, err := New(t.Context(), f.socket); err == nil {
		t.Fatal("accepted missing provider id")
	}
	if _, err := New(t.Context(), filepath.Join(t.TempDir(), "absent")); !errors.Is(err, platforms.ErrPlatformUnreachable) {
		t.Fatal(err)
	}
}

func TestHTTPErrorContract(t *testing.T) {
	f := newFake(t, allCapabilities())
	a := f.adapter(t)
	for _, tt := range []struct {
		status int
		target error
	}{{404, platforms.ErrNotFound}, {409, platforms.ErrBusy}, {501, platforms.ErrUnsupported}, {422, platforms.ErrUpstreamRejected}, {500, nil}, {302, nil}} {
		f.mu.Lock()
		f.replies["POST /sessions/s/send"] = fakeReply{tt.status, providerprotocol.Error{Message: "reason"}}
		f.mu.Unlock()
		err := a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "s"})
		if err == nil || (tt.target != nil && !errors.Is(err, tt.target)) {
			t.Fatal(tt.status, err)
		}
		if tt.status == 422 {
			var upstream *platforms.UpstreamError
			if !errors.As(err, &upstream) || upstream.Message != "reason" {
				t.Fatal(err)
			}
		}
	}
	f.reply("GET", "/sessions/s", platforms.SessionDetail{})
	if _, err := a.RuntimeDetail(t.Context(), "s", 1, 0); err == nil {
		t.Fatal("accepted missing detail")
	}
	f.reply("POST", "/sessions", platforms.CreateSessionResponse{})
	if _, err := a.CreateSession(t.Context(), platforms.CreateSessionRequest{}); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if a.Available(ctx) {
		t.Fatal("available with cancelled context")
	}
	if err := a.SendMessage(t.Context(), platforms.SendMessageRequest{Delivery: "unknown"}); !errors.Is(err, platforms.ErrUnsupported) {
		t.Fatal(err)
	}
}

// Configured creates need a disposer; providers cannot delete, so it reports.
func TestDisposeSessionReports(t *testing.T) {
	var _ platforms.SessionDisposer = (*Adapter)(nil)
	if err := (&Adapter{}).DisposeSession(t.Context(), platforms.DisposeSessionRequest{SessionID: "s"}); err == nil {
		t.Fatal("dispose claimed to delete a provider session")
	}
}

// A supervisor outside the protocol range is reached only through its
// version-independent routes while it drains: the headless event stream, of
// which only prompt events are read (pushed, never polled), their replies and
// abort. Nothing else is sent, and no turn is acknowledged.
func TestDrainingAdapterUsesOnlyVersionIndependentRoutes(t *testing.T) {
	f := newFake(t, allCapabilities())
	f.hello.ProtocolVersion = providerprotocol.CurrentVersion + 7
	if _, err := New(t.Context(), f.socket); err == nil {
		t.Fatal("an out-of-range supervisor must be refused by New")
	}
	f.events = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("consumer") != "headless" {
			t.Errorf("draining stream = %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i, e := range []string{
			`{"type":"session.status","properties":{"sessionID":"s","status":{"type":"busy"}}}`,
			`{"type":"permission.asked","properties":{"id":"p1","sessionID":"s","permission":"bash","patterns":["make"]}}`,
			`{"type":"question.asked","properties":{"id":"q1","sessionID":"s","questions":[]}}`,
			`{"type":"session.idle","properties":{"sessionID":"s"}}`,
		} {
			_, _ = fmt.Fprintf(w, "id: %d\ndata: %s\n\n", i+1, e)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	a, err := NewDraining(t.Context(), f.socket)
	requireNoError(t, err)
	defer a.Close()
	var seen []string
	var mu sync.Mutex
	a.SetEventHandler(func(e providerprotocol.Event) { mu.Lock(); seen = append(seen, e.Type); mu.Unlock() })
	idles := 0
	a.SetSessionEventSink(platforms.SessionEventSink{OnSessionIdle: func(string, string) { idles++ }})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { _ = a.WatchEvents(ctx) }()
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		q, _ := a.ListQuestions(t.Context(), "s")
		if len(q) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("prompts never arrived on the draining stream")
		}
	}
	if got, err := a.ListPermissions(t.Context(), "s"); err != nil || len(got) != 1 {
		t.Fatalf("permissions = %+v %v", got, err)
	}
	mu.Lock()
	if !slices.Equal(seen, []string{"permission.asked", "question.asked"}) || idles != 0 {
		t.Fatalf("draining adapter read versioned events: %v idles=%d", seen, idles)
	}
	mu.Unlock()
	requireNoError(t, a.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: "s", PermissionID: "p1", Reply: "once"}))
	requireNoError(t, a.RejectQuestion(t.Context(), platforms.RejectQuestionRequest{SessionID: "s", RequestID: "q1"}))
	requireNoError(t, a.Abort(t.Context(), platforms.AbortRequest{SessionID: "s"}))
	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
	calls := count()
	for _, err := range []error{
		a.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: "s", Message: "x"}),
		func() error { _, err := a.Sessions(t.Context(), "", 0); return err }(),
		func() error { _, err := a.RuntimeDetail(t.Context(), "s", 1, 0); return err }(),
		func() error { _, err := a.LiveSessions(t.Context()); return err }(),
		a.ProxyEvents(t.Context(), "s", io.Discard, nil),
	} {
		if !errors.Is(err, platforms.ErrUnsupported) {
			t.Fatalf("draining adapter allowed a versioned route: %v", err)
		}
	}
	if count() != calls {
		t.Fatal("versioned routes reached the supervisor")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c.method == "GET" && (c.path == "/permissions" || c.path == "/questions") || strings.HasSuffix(c.path, "/ack") {
			t.Fatalf("draining adapter polled or acknowledged: %+v", c)
		}
	}
}
