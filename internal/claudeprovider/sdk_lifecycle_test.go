package claudeprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestSDKIdleBeforeResultAndBackgroundContinuation(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"idle"}`))
	if !p.busy {
		t.Fatal("idle without a result prematurely settled")
	}
	l.result(p, []byte(`{"subtype":"success"}`))
	if p.busy {
		t.Fatal("result following idle did not settle")
	}
	if err := l.handle(p, []byte(`{"type":"assistant","message":{"role":"assistant","id":"followup","content":[{"type":"text","text":"continued"}]}}`)); err != nil {
		t.Fatal(err)
	}
	if !p.busy || l.status[p.id].Status != db.StatusBusy || l.accepted[p.id] != 1 {
		t.Fatal("background continuation did not reopen supervisor ownership")
	}
	l.result(p, []byte(`{"subtype":"success"}`))
	if p.busy {
		t.Fatal("continuation never settled")
	}
}

func TestSDKTaskLifecycleVariants(t *testing.T) {
	for _, taskType := range []string{"local_agent", "local_workflow", "local_bash", "remote_agent"} {
		for _, status := range []string{"completed", "failed", "stopped"} {
			l, p := sdkLifecycle(t)
			l.system(p, []byte(`{"subtype":"task_started","task_type":"`+taskType+`","task_id":"task"}`))
			l.result(p, []byte(`{"subtype":"success"}`))
			tracked := taskType == "local_agent" || taskType == "local_workflow"
			if p.busy != tracked {
				t.Fatalf("%s busy=%v", taskType, p.busy)
			}
			l.system(p, []byte(`{"subtype":"task_updated","task_id":"task","patch":{"status":"running"}}`))
			if p.busy != tracked {
				t.Fatal("nonterminal task update settled")
			}
			l.system(p, []byte(`{"subtype":"task_updated","task_id":"task","patch":{"status":"`+status+`"}}`))
			if p.busy {
				t.Fatalf("%s did not settle", status)
			}
		}
	}
}

func TestSDKRunEndCeiling(t *testing.T) {
	for input, want := range map[string]time.Duration{"": 10 * time.Minute, "bad": 10 * time.Minute, "-1": 10 * time.Minute, "0": 0, "25": 25 * time.Millisecond, "9999999999": time.Duration(1<<31-1) * time.Millisecond} {
		t.Setenv("CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS", input)
		if got := runEndCeiling(); got != want {
			t.Fatalf("ceiling(%q)=%v, want %v", input, got, want)
		}
	}
	t.Setenv("CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS", "10")
	l, p := sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"running"}`))
	l.result(p, []byte(`{"subtype":"success"}`))
	waitFor(t, "bounded run end", func() bool { l.mu.Lock(); defer l.mu.Unlock(); return !p.busy })
	for _, event := range []string{
		`{"subtype":"session_state_changed","state":"requires_action"}`,
		`{"subtype":"task_started","task_type":"local_agent","task_id":"task"}`,
	} {
		l, p := sdkLifecycle(t)
		l.system(p, []byte(`{"subtype":"session_state_changed","state":"running"}`))
		l.system(p, []byte(event))
		l.result(p, []byte(`{"subtype":"success"}`))
		if p.runCeiling != nil {
			t.Fatal("live task or permission wait armed a ceiling")
		}
	}
	t.Setenv("CLAUDE_CODE_PRINT_BG_WAIT_CEILING_MS", "0")
	l, p = sdkLifecycle(t)
	l.system(p, []byte(`{"subtype":"session_state_changed","state":"running"}`))
	l.result(p, []byte(`{"subtype":"success"}`))
	if p.runCeiling != nil {
		t.Fatal("zero ceiling armed a timer")
	}
}

func TestSDKDeferredResultCannotSettleNewerTurn(t *testing.T) {
	l, p := sdkLifecycle(t)
	old := uint64(0)
	l.accepted[p.id] = 1
	l.resultFor(p, []byte(`{"subtype":"success"}`), &old)
	if !p.busy {
		t.Fatal("stale deferred result settled a newer turn")
	}
}

func TestSDKInterruptAcknowledgmentAndRetainedInputs(t *testing.T) {
	for _, queued := range []string{`[]`, `["queued"]`, `[{"uuid":"queued"}]`} {
		l, p := sdkLifecycle(t)
		p.done, p.controls, p.stderr = make(chan struct{}), map[string]chan controlResult{}, &tail{}
		p.pending["queued"] = true
		p.stdin = sdkWriter{func(b []byte) (int, error) {
			var request struct {
				ID string `json:"request_id"`
			}
			_ = json.Unmarshal(b, &request)
			// A result can precede the interrupt acknowledgment.
			l.result(p, []byte(`{"subtype":"error_during_execution","is_error":true}`))
			l.receiveControl(p, []byte(`{"response":{"subtype":"success","request_id":"`+request.ID+`","response":{"still_queued":`+queued+`}}}`))
			return len(b), nil
		}}
		if err := l.abort(t.Context(), p.id); err != nil {
			t.Fatal(err)
		}
		if p.busy != (queued != `[]`) {
			t.Fatalf("queued=%s busy=%v", queued, p.busy)
		}
		if queued != `[]` {
			l.started(p, "queued")
			l.result(p, []byte(`{"subtype":"error_during_execution","is_error":true,"errors":["queued turn failed"]}`))
			if l.status[p.id].Status != db.StatusError {
				t.Fatal("retained input failure was mistaken for the earlier abort")
			}
		}
	}
	l, p := sdkLifecycle(t)
	p.done, p.controls, p.stderr = make(chan struct{}), map[string]chan controlResult{}, &tail{}
	p.stdin = sdkWriter{func(b []byte) (int, error) {
		var request struct {
			ID string `json:"request_id"`
		}
		_ = json.Unmarshal(b, &request)
		l.receiveControl(p, []byte(`{"response":{"subtype":"error","request_id":"`+request.ID+`","error":"interrupt denied"}}`))
		return len(b), nil
	}}
	if err := l.abort(context.Background(), p.id); err == nil || !strings.Contains(err.Error(), "interrupt denied") {
		t.Fatalf("abort=%v", err)
	}
	if p.aborted || !p.busy {
		t.Fatal("rejected interrupt changed the turn outcome")
	}
}

func TestSDKStartupFailureAndStreamExit(t *testing.T) {
	for name, rows := range map[string]string{
		"startup result": `{"direction":"stdin","message":{"type":"control_request","request":{"subtype":"initialize"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":true,"result":"API Error: startup refused"}}
`,
		"corrupt stream": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"raw","message":"{broken JSON"}
`,
		"background crash": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"system","subtype":"session_state_changed","state":"running"}}
{"direction":"stdout","message":{"type":"result","subtype":"success"}}
{"direction":"exit","message":4}
`,
		"deferred API error": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"system","subtype":"session_state_changed","state":"running"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":true,"result":"API Error: turn refused"}}
{"direction":"exit","message":1}
`,
	} {
		t.Run(name, func(t *testing.T) {
			stdin := t.TempDir() + "/stdin"
			t.Setenv("FAKE_CLAUDE_STDIN", stdin)
			h := startLive(t, writeFixture(t, rows), nil)
			id := h.create(t)
			h.send(t, id, "hello")
			waitFor(t, "failed run idle", func() bool { s, _, n := h.events.get(id); return s == db.StatusError && n == 1 })
			if name == "startup result" {
				if !h.visible(id, "API Error: startup refused") || strings.Contains(string(must(os.ReadFile(stdin))), `"type":"user"`) {
					t.Fatal("startup error was lost or prompt delivered")
				}
			}
			if name == "corrupt stream" && !h.visible(id, "invalid Claude stream JSON") {
				t.Fatal("stream corruption not visible")
			}
			if name == "deferred API error" && !h.visible(id, "API Error: turn refused") {
				t.Fatal("API error replaced by a generic exit error")
			}
		})
	}
}

func TestSDKPermissionContextIsPreserved(t *testing.T) {
	l, p := sdkLifecycle(t)
	l.ask(p, []byte(`{"request_id":"permission","request":{"subtype":"can_use_tool","tool_name":"Read","input":{"file_path":"/tmp/file"},"blocked_path":"/tmp/file","decision_reason":"outside project","title":"Read file","display_name":"Read","description":"Needs approval","permission_suggestions":[{"type":"addDirectories"}]}}`))
	for _, prompt := range l.prompts {
		metadata := prompt.props["metadata"].(map[string]any)
		if metadata["blocked_path"] != "/tmp/file" || metadata["decision_reason"] != "outside project" || metadata["title"] != "Read file" || metadata["display_name"] != "Read" || metadata["description"] != "Needs approval" || len(metadata["permission_suggestions"].([]any)) != 1 {
			t.Fatal("SDK permission context was discarded")
		}
		return
	}
	t.Fatal("permission not published")
}

func TestSDKContinuationSpoolFailureStopsChild(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "continue")
	h := startLive(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success"}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"assistant","message":{"role":"assistant","id":"continuation","content":[{"type":"text","text":"continued"}]}}}
`, gate)), nil)
	id := h.create(t)
	h.send(t, id, "first")
	waitFor(t, "initial result", func() bool { _, _, n := h.events.get(id); return n == 1 })
	restore := h.spoolDown(t, id)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "unpublishable continuation stopped", func() bool { return h.procs() == 0 })
	restore()
	waitFor(t, "failed continuation settled", func() bool { s, _, n := h.events.get(id); return s == db.StatusError && n == 2 })
}
