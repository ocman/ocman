package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/ocruntime"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/claude/provider/claudetest"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
	"github.com/NoUseFreak/ocman/internal/providersupervisor"
	"github.com/NoUseFreak/ocman/internal/state"
)

// oldSupervisor is a real supervisor speaking protocol 99: it spools and
// streams events like any other, but the dashboard may use only its
// version-independent routes.
type oldSupervisor struct {
	sup     *providersupervisor.Supervisor
	mu      sync.Mutex
	routes  []string
	replies map[string]string // prompt id -> reply received
}

func (o *oldSupervisor) publish(t *testing.T, typ string, props map[string]any) {
	t.Helper()
	data, _ := json.Marshal(props)
	if err := o.sup.Publish(providerprotocol.Event{Type: typ, Properties: data}, nil); err != nil {
		t.Fatal(err)
	}
}

func (o *oldSupervisor) reply(id string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.replies[id]
}

// drainEnv is a persisted out-of-range supervisor (protocol 99) mid-turn
// on session drainSession, with one pending permission (per_old).
type drainEnv struct {
	*recoveryEnv
	old      *oldSupervisor
	endpoint string
	exited   chan struct{}
}

const drainSession = "11111111-2222-4333-8444-555555555555"

func drainPermission(id, command string) map[string]any {
	return map[string]any{"id": id, "sessionID": drainSession, "permission": "bash", "patterns": []string{command}, "always": []string{command},
		"metadata": map[string]any{"command": command}}
}

func newDrainEnv(t *testing.T) *drainEnv {
	t.Helper()
	e := newRecoveryEnv(t)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.data, "config"))
	t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
	const session = drainSession

	old := &oldSupervisor{replies: map[string]string{}}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		old.mu.Lock()
		old.routes = append(old.routes, r.Method+" "+r.URL.Path)
		old.mu.Unlock()
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Method != http.MethodPost || len(parts) != 3 || (parts[0] != "permissions" && parts[0] != "questions") {
			t.Errorf("out-of-range supervisor got %s %s: only version-independent routes may be used", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotImplemented)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply, _ := body["Reply"].(string)
		typ := "permission.replied"
		switch {
		case parts[0] == "questions" && parts[2] == "reject":
			typ, reply = "question.rejected", "reject"
		case parts[0] == "questions":
			typ, reply = "question.replied", "answered"
		}
		old.mu.Lock()
		old.replies[parts[1]] = reply
		old.mu.Unlock()
		old.publish(t, typ, map[string]any{"sessionID": session, "requestID": parts[1], "reply": reply})
		w.WriteHeader(http.StatusNoContent)
	})
	sup, err := providersupervisor.New(filepath.Join(e.data, "old-spool"), providersupervisor.Backend{
		Hello:   providerprotocol.Hello{ProtocolVersion: 99, PlatformID: "claudecode", CLIVersion: "2.1.220"},
		Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	old.sup = sup
	socket := filepath.Join(e.data, "old.sock")
	go func() { _ = sup.Serve(t.Context(), socket) }()
	eventually(t, "old supervisor listening", func() bool { _, err := os.Stat(socket); return err == nil })
	if err := sup.BeginTurn(session, nil); err != nil {
		t.Fatal(err)
	}
	old.publish(t, "permission.asked", drainPermission("per_old", "make"))

	// It is persisted as running under the fake tmux (a sleep stands in).
	sleeper := exec.Command("sleep", "300")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill() })
	exited := make(chan struct{})
	go func() { _ = sleeper.Wait(); close(exited) }()
	const name = "claudecode-aaaaaaaaaaaa-bbbbbbbb-cccccccccccc"
	if err := os.WriteFile(filepath.Join(e.data, "pids", name+".pid"), []byte(strconv.Itoa(sleeper.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := "unix://" + socket
	if err := e.state.UpsertManagedProvider(t.Context(), "claudecode", e.repo, state.ManagedInstance{Endpoint: endpoint, Kind: ocruntime.KindProviderTmux, RuntimeID: name, LaunchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	owned, _ := json.Marshal(map[string]any{session: map[string]any{"directory": e.repo, "created": 1, "started": true}})
	if err := os.WriteFile(filepath.Join(e.data, "claude-sessions.json"), owned, 0o600); err != nil {
		t.Fatal(err)
	}

	return &drainEnv{recoveryEnv: e, old: old, endpoint: endpoint, exited: exited}
}

// A dashboard restart that finds an out-of-range supervisor mid-turn keeps
// its prompts on the normal dashboard flow while it drains: the pending
// permission and every later permission and question are pushed to the
// prompt pipeline and the session's browser stream (never polled), their
// resolutions reach the browser, Allow-always is recorded in ocman's rules
// (and decides the next identical prompt), new turns are refused, and the
// supervisor is replaced only once it settles (AD-5).
func TestOutOfRangeSupervisorDeliversPromptsWhileDraining(t *testing.T) {
	d := newDrainEnv(t)
	e, old, sup, endpoint, exited := d.recoveryEnv, d.old, d.old.sup, d.endpoint, d.exited
	const session = drainSession
	permission := drainPermission

	// The dashboard: its prompt pipeline and an open browser stream.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := newClaudePlatformAt(ctx, nil, e.state, e.data, e.exe, nil, "")
	if p == nil {
		t.Fatal("Claude platform did not start")
	}
	var mu sync.Mutex
	var pipeline []string
	var browser bytes.Buffer
	p.SetEventHandler(func(ev providerprotocol.Event) {
		var props map[string]any
		_ = json.Unmarshal(ev.Properties, &props)
		mu.Lock()
		pipeline = append(pipeline, strings.TrimSpace(ev.Type+" "+asString(props["id"])+asString(props["requestID"])+" "+asString(props["reply"])))
		mu.Unlock()
	})
	p.SetMCPAddr("127.0.0.1:47301")
	go func() { _ = p.WatchEvents(ctx) }()
	saw := func(what string, ok func() bool) {
		t.Helper()
		eventually(t, what, func() bool { mu.Lock(); defer mu.Unlock(); return ok() })
	}
	piped := func(entry string) bool { return slices.Contains(pipeline, entry) }
	streamed := func(text string) bool { return strings.Contains(browser.String(), text) }
	eventually(t, "drain started", func() bool { return p.projects.drainingFor(ctx, session) != nil })
	go func() {
		_ = p.ProxyEvents(ctx, session, syncWriter{&mu, &browser}, nil)
	}()
	saw("pending permission pushed", func() bool { return piped("permission.asked per_old") && streamed(`"id":"per_old"`) })
	if listed, err := p.ListPermissions(ctx, session); err != nil || len(listed) != 1 || listed[0]["id"] != "per_old" {
		t.Fatalf("pending = %+v %v", listed, err)
	}
	time.Sleep(1500 * time.Millisecond) // more than one drain poll
	if e.endpoint(t) != endpoint {
		t.Fatal("the supervisor was replaced before it settled")
	}
	if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: session, Message: "hi"}); !errors.Is(err, platforms.ErrBusy) {
		t.Fatalf("send during the drain = %v, want busy", err)
	}

	// Allow always: recorded in ocman's rules before the reply.
	if err := p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: session, PermissionID: "per_old", Reply: "always"}); err != nil {
		t.Fatal(err)
	}
	// The pipeline records the user's always reply, which worktree children
	// inherit (auto_approved_permission), and the browser sees it resolve.
	saw("resolution visible", func() bool { return piped("permission.replied per_old always") && streamed(`"requestID":"per_old"`) })
	if data, _ := os.ReadFile(filepath.Join(e.data, "claude-rules.json")); !strings.Contains(string(data), `"make"`) {
		t.Fatalf("Allow-always not persisted: %s", data)
	}

	// A later permission in the same turn, then a question.
	old.publish(t, "permission.asked", permission("per_two", "make test"))
	saw("second permission pushed", func() bool { return piped("permission.asked per_two") && streamed(`"id":"per_two"`) })
	if err := p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: session, PermissionID: "per_two", Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	old.publish(t, "question.asked", map[string]any{"id": "que_one", "sessionID": session,
		"questions": []map[string]any{{"question": "Which?", "header": "h", "options": []any{}, "multiple": false, "custom": true}}})
	saw("question pushed", func() bool { return piped("question.asked que_one") && streamed(`"id":"que_one"`) })
	if q, err := p.ListQuestions(ctx, session); err != nil || len(q) != 1 {
		t.Fatalf("questions = %+v %v", q, err)
	}
	if err := p.RespondQuestion(ctx, platforms.RespondQuestionRequest{SessionID: session, RequestID: "que_one", Answers: [][]string{{"a"}}}); err != nil {
		t.Fatal(err)
	}
	saw("question resolved", func() bool { return streamed(`"type":"question.replied"`) })

	// The rule decides the next identical prompt: never shown, never judged.
	old.publish(t, "permission.asked", permission("per_ruled", "make"))
	eventually(t, "rule answered", func() bool { return old.reply("per_ruled") == "once" })
	mu.Lock()
	if piped("permission.asked per_ruled") || streamed(`"id":"per_ruled"`) {
		t.Fatal("a rule-allowed prompt reached the pipeline or browser")
	}
	mu.Unlock()
	if old.reply("per_two") != "once" {
		t.Fatalf("replies = %+v", old.replies)
	}

	idle, _ := json.Marshal(map[string]any{"sessionID": session})
	if err := sup.Publish(providerprotocol.Event{Type: "session.idle", Properties: idle}, nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "replaced after settling", func() bool { return e.endpoint(t) != "" && e.endpoint(t) != endpoint })
	<-exited
	eventually(t, "send on the replacement", func() bool {
		return p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: session, Message: "hi"}) == nil
	})
	old.mu.Lock()
	defer old.mu.Unlock()
	for _, r := range old.routes {
		if strings.HasPrefix(r, "GET ") {
			t.Fatalf("prompts were polled: %v", old.routes)
		}
	}
}

// The dashboard may register its event handler and start watching only
// after a project supervisor attached or recovery found one draining: both
// still reach the pipeline, with no event stream started twice.
func TestLateWatchReachesAttachedAndDrainingSupervisors(t *testing.T) {
	late := func(t *testing.T, p *claudePlatform, ctx context.Context) func(string) bool {
		var mu sync.Mutex
		var seen []string
		p.SetEventHandler(func(ev providerprotocol.Event) {
			mu.Lock()
			seen = append(seen, ev.Type+" "+string(ev.Properties))
			mu.Unlock()
		})
		go func() { _ = p.WatchEvents(ctx) }()
		return func(text string) bool {
			mu.Lock()
			defer mu.Unlock()
			return slices.ContainsFunc(seen, func(s string) bool { return strings.Contains(s, text) })
		}
	}
	t.Run("attached", func(t *testing.T) {
		e := newRecoveryEnv(t)
		t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.data, "config"))
		t.Setenv("FAKE_CLAUDE_FIXTURE", claudetest.Fixture("plain.jsonl"))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		p := newClaudePlatformAt(ctx, nil, e.state, e.data, e.exe, nil, "")
		if p == nil {
			t.Fatal("Claude platform did not start")
		}
		p.SetMCPAddr("127.0.0.1:47303")
		created, err := p.CreateSession(ctx, platforms.CreateSessionRequest{Directory: e.repo})
		if err != nil {
			t.Fatal(err)
		}
		seen := late(t, p, ctx)
		if err := p.SendMessage(ctx, platforms.SendMessageRequest{SessionID: created.ID, Message: "hi"}); err != nil {
			t.Fatal(err)
		}
		eventually(t, "the attached supervisor's events", func() bool { return seen(`session.idle {"sessionID":"` + created.ID) })
	})
	t.Run("draining", func(t *testing.T) {
		d := newDrainEnv(t)
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		p := newClaudePlatformAt(ctx, nil, d.state, d.data, d.exe, nil, "")
		if p == nil {
			t.Fatal("Claude platform did not start")
		}
		p.SetMCPAddr("127.0.0.1:47304")
		eventually(t, "drain started", func() bool { return p.projects.drainingFor(ctx, drainSession) != nil })
		seen := late(t, p, ctx)
		d.old.publish(t, "permission.asked", drainPermission("per_late", "make"))
		eventually(t, "the draining supervisor's prompt", func() bool { return seen(`"id":"per_late"`) })
	})
}

func asString(v any) string {
	if s, _ := v.(string); s != "" {
		return s
	}
	return ""
}

type syncWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (s syncWriter) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(b)
}

// The inverse ordering: a browser already streaming the session when the
// restarted dashboard's asynchronous recovery finds its supervisor out of
// range. The same stream switches to the draining supervisor without a
// reconnect or any polling: it receives the pending permission, later
// permissions and questions, and their resolutions. Discovery lands either
// once the browsing stream runs, or between the stream's route lookup and
// its subscription to attach notifications.
func TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts(t *testing.T) {
	for _, point := range []string{"browsing", "routed"} {
		t.Run(point, func(t *testing.T) { browserBeforeDrain(t, point) })
	}
}

func browserBeforeDrain(t *testing.T, point string) {
	d := newDrainEnv(t)
	browsing, proceed := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(browsing) })
	discover := sync.OnceFunc(func() { close(proceed) })
	t.Cleanup(release)
	t.Cleanup(discover)
	var platform atomic.Pointer[claudePlatform]
	old := claudeTestHook
	claudeTestHook = func(hook, key string) {
		switch {
		case hook == point && key == drainSession && point == "browsing":
			release()
		case hook == point && key == drainSession:
			// Recovery reports the drain before this stream subscribes.
			discover()
			for platform.Load().projects.drainingFor(context.Background(), drainSession) == nil {
				time.Sleep(time.Millisecond)
			}
			release()
		case hook == "drain":
			<-proceed // recovery reports the drain only after the browser streams
		}
	}
	t.Cleanup(func() { claudeTestHook = old })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := newClaudePlatformAt(ctx, nil, d.state, d.data, d.exe, nil, "")
	if p == nil {
		t.Fatal("Claude platform did not start")
	}
	platform.Store(p)
	var mu sync.Mutex
	var pipeline []string
	var browser bytes.Buffer
	p.SetEventHandler(func(ev providerprotocol.Event) {
		var props map[string]any
		_ = json.Unmarshal(ev.Properties, &props)
		mu.Lock()
		pipeline = append(pipeline, strings.TrimSpace(ev.Type+" "+asString(props["id"])+asString(props["requestID"])))
		mu.Unlock()
	})
	p.SetMCPAddr("127.0.0.1:47302")
	go func() { _ = p.WatchEvents(ctx) }()
	streamDone := make(chan error, 1)
	go func() { streamDone <- p.ProxyEvents(ctx, drainSession, syncWriter{&mu, &browser}, nil) }()
	<-browsing
	if point == "browsing" && p.projects.drainingFor(ctx, drainSession) != nil {
		t.Fatal("the drain was discovered before the browser connected")
	}
	discover()
	saw := func(what string, ok func() bool) {
		t.Helper()
		eventually(t, what, func() bool { mu.Lock(); defer mu.Unlock(); return ok() })
	}
	streamed := func(text string) bool { return strings.Contains(browser.String(), text) }
	piped := func(entry string) bool { return slices.Contains(pipeline, entry) }

	saw("pending permission on the open stream", func() bool { return streamed(`"id":"per_old"`) && piped("permission.asked per_old") })
	if err := p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: drainSession, PermissionID: "per_old", Reply: "once"}); err != nil {
		t.Fatal(err)
	}
	saw("resolution on the open stream", func() bool { return streamed(`"requestID":"per_old"`) })
	d.old.publish(t, "permission.asked", drainPermission("per_two", "make test"))
	saw("later permission on the open stream", func() bool { return streamed(`"id":"per_two"`) && piped("permission.asked per_two") })
	if err := p.RespondPermission(ctx, platforms.RespondPermissionRequest{SessionID: drainSession, PermissionID: "per_two", Reply: "reject"}); err != nil {
		t.Fatal(err)
	}
	saw("later resolution on the open stream", func() bool { return streamed(`"requestID":"per_two"`) })
	d.old.publish(t, "question.asked", map[string]any{"id": "que_one", "sessionID": drainSession,
		"questions": []map[string]any{{"question": "Which?", "header": "h", "options": []any{}, "multiple": false, "custom": true}}})
	saw("question on the open stream", func() bool { return streamed(`"id":"que_one"`) && piped("question.asked que_one") })
	if err := p.RejectQuestion(ctx, platforms.RejectQuestionRequest{SessionID: drainSession, RequestID: "que_one"}); err != nil {
		t.Fatal(err)
	}
	saw("question resolution on the open stream", func() bool { return streamed(`"type":"question.rejected"`) })
	select {
	case err := <-streamDone:
		t.Fatalf("the browser stream ended (%v): it must carry the drain without a reconnect", err)
	default:
	}
	d.old.mu.Lock()
	defer d.old.mu.Unlock()
	if d.old.replies["per_old"] != "once" || d.old.replies["per_two"] != "reject" || d.old.replies["que_one"] != "reject" {
		t.Fatalf("replies = %+v", d.old.replies)
	}
	for _, r := range d.old.routes {
		if strings.HasPrefix(r, "GET ") {
			t.Fatalf("prompts were polled: %v", d.old.routes)
		}
	}
}
