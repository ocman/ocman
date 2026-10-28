package claudeprovider

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"github.com/ocman/ocman/internal/providersupervisor"
)

func TestSDKChildEnvironment(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("CLAUDE_AGENT_SDK_VERSION", "stale")
	t.Setenv("CLAUDE_CONFIG_DIR", "/inherited")
	for _, config := range []*string{nil, new(""), new("/chosen")} {
		env := (Options{ConfigEnv: config}).childEnv()
		if env == nil {
			env = os.Environ()
		}
		values := map[string]string{}
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			values[k] = v
		}
		if values["CLAUDE_CODE_ENTRYPOINT"] != "sdk-py" || values["CLAUDE_AGENT_SDK_VERSION"] != "0.2.166" ||
			values["CLAUDE_CODE_SDK_READS_SESSION_STATE"] != "1" {
			t.Fatal("SDK markers missing")
		}
		if _, ok := values["CLAUDECODE"]; ok {
			t.Fatal("nested-Claude marker inherited")
		}
		want := "/inherited"
		if config != nil {
			want = *config
		}
		if values["CLAUDE_CONFIG_DIR"] != want {
			t.Fatalf("config = %q, want %q", values["CLAUDE_CONFIG_DIR"], want)
		}
	}
}

func sdkLifecycle(t *testing.T) (*live, *proc) {
	t.Helper()
	l := newLive(nil, Options{IdleTimeout: time.Hour}, "2.1.289")
	sup, err := providersupervisor.New(t.TempDir(), providersupervisor.Backend{})
	if err != nil {
		t.Fatal(err)
	}
	l.sup = sup
	l.owned["test"] = &owned{Directory: t.TempDir(), Started: true}
	p := &proc{id: "test", busy: true, pending: map[string]bool{}, children: map[string]*Converter{}, conv: &Converter{SessionID: "test"}}
	l.procs[p.id] = p
	l.status[p.id] = db.Session{Status: db.StatusBusy}
	t.Cleanup(func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.stopRunCeilingLocked(p)
		if p.idle != nil {
			p.idle.Stop()
		}
	})
	return l, p
}

func TestSDKResultWaitsForSessionIdle(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"running"}`))
	l.result(p, []byte(`{"subtype":"success","result":"first"}`))
	if !p.busy || l.status[p.id].Status != db.StatusBusy {
		t.Fatal("a result settled while Claude still reports running")
	}
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"requires_action"}`))
	if !p.busy {
		t.Fatal("permission wait settled the run")
	}
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"idle"}`))
	if p.busy || l.status[p.id].Status != db.StatusDone {
		t.Fatal("idle after result did not settle")
	}
}

func TestSDKBackgroundTasksDoNotHoldForegroundOpen(t *testing.T) {
	for _, terminal := range []string{
		`{"subtype":"task_notification","task_id":"background"}`,
		`{"subtype":"task_updated","task_id":"background","patch":{"status":"completed"}}`,
	} {
		l, p := sdkLifecycle(t)
		l.system(p, []byte(`{"subtype":"task_started","task_id":"background","task_type":"local_agent"}`))
		l.result(p, []byte(`{"subtype":"success"}`))
		if p.busy || len(p.tasks) != 1 {
			t.Fatal("foreground result must settle without discarding background work")
		}
		l.system(p, []byte(terminal))
		if p.busy {
			t.Fatal("completed task left the result stranded")
		}
	}
}

func TestSDKInitializeBeforeFirstPrompt(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "ack")
	stdin := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("FAKE_CLAUDE_STDIN", stdin)
	rows := `{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"initialize"}}}
{"direction":"wait","message":` + string(must(json.Marshal(gate))) + `}
{"direction":"stdout","message":{"type":"control_response","response":{"subtype":"success","request_id":"ocman-initialize","response":{}}}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success"}}
`
	h := startLive(t, writeFixture(t, rows), nil)
	id := h.create(t)
	h.send(t, id, "hello")
	waitFor(t, "initialize", func() bool { b, _ := os.ReadFile(stdin); return len(b) > 0 })
	b, _ := os.ReadFile(stdin)
	if !strings.Contains(string(b), `"subtype":"initialize"`) || strings.Contains(string(b), `"type":"user"`) {
		t.Fatalf("first input was not initialize: %s", b)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "initialized turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	h.live.mu.Lock()
	env := slices.Clone(h.live.procs[id].cmd.Env)
	h.live.mu.Unlock()
	if !slices.Contains(env, "PWD="+h.dir) {
		t.Fatal("child PWD does not match its directory")
	}
}

func TestSDKInitializationFailureDoesNotSendPrompt(t *testing.T) {
	stdin := filepath.Join(t.TempDir(), "stdin")
	t.Setenv("FAKE_CLAUDE_STDIN", stdin)
	h := startLive(t, writeFixture(t, `{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"initialize"}}}
{"direction":"stdout","message":{"type":"control_response","response":{"subtype":"error","request_id":"ocman-initialize","error":"MCP initialization failed"}}}
`), nil)
	id := h.create(t)
	h.send(t, id, "never deliver this")
	waitFor(t, "failed initialization", func() bool { s, _, n := h.events.get(id); return s == db.StatusError && n == 1 })
	if !h.visible(id, "MCP initialization failed") {
		t.Fatal("initialization error was lost")
	}
	if strings.Contains(string(must(os.ReadFile(stdin))), `"type":"user"`) {
		t.Fatal("prompt sent after failed initialization")
	}
}

func TestSDKModelChangeOnLiveChild(t *testing.T) {
	h := startLive(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"working","content":[{"type":"text","text":"WORKING"}]}}}
{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"set_model"}}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:2","message":{"role":"user","content":"second"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success"}}
`), nil)
	id := h.create(t)
	h.send(t, id, "first")
	waitFor(t, "first reply", func() bool { return h.visible(id, "WORKING") })
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "second", Model: "anthropic/opus"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "model switched", func() bool { _, _, n := h.events.get(id); return n == 1 })
	h.live.mu.Lock()
	p := h.live.procs[id]
	model := "<exited>"
	if p != nil {
		model = p.model
	}
	h.live.mu.Unlock()
	if model != "opus" {
		t.Fatalf("live child kept model %q", model)
	}
}
