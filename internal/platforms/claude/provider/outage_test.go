package claudeprovider

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
)

func fastRetries(t *testing.T) {
	setRecordRetry(t, 20*time.Millisecond)
	old := settleRetry.Swap(int64(20 * time.Millisecond))
	t.Cleanup(func() { settleRetry.Store(old) })
}

// supervisorActive reports whether the supervisor still holds the session's
// turn or a pending prompt: what blocks /drain and spool truncation.
func (h *liveHarness) supervisorActive(id string) bool {
	return h.live.sup.Active(id)
}

// holdOutage keeps the spool down long enough for every retry to fail
// repeatedly, and checks nothing settled meanwhile.
func holdOutage(t *testing.T, h *liveHarness, id string) {
	t.Helper()
	waitFor(t, "child gone", func() bool { return h.procs() == 0 })
	time.Sleep(300 * time.Millisecond)
	if _, _, idles := h.events.get(id); idles != 0 {
		t.Fatal("an idle edge was delivered while the spool could not record the turn")
	}
	if !h.supervisorActive(id) {
		t.Fatal("the supervisor released a turn whose end was never recorded")
	}
}

// expectSettled checks the durable end state after the spool is restored:
// one error idle edge for the queue and routines, nothing pending, the
// supervisor's turn released and the supervisor drainable.
func expectSettled(t *testing.T, h *liveHarness, id string) {
	t.Helper()
	waitFor(t, "idle edge after restore", func() bool { _, _, n := h.events.get(id); return n == 1 })
	waitFor(t, "supervisor released", func() bool { return !h.supervisorActive(id) })
	if s, _, _ := h.events.get(id); s != db.StatusError {
		t.Fatalf("status = %s, want the durable error", s)
	}
	if life, err := h.adapter.SessionLifecycle(t.Context(), id); err != nil || life.Status != db.StatusError {
		t.Fatalf("lifecycle = %+v %v", life, err)
	}
	if p := h.live.pendingPrompts(id, false); len(p) != 0 {
		t.Fatalf("pending = %+v", p)
	}
	time.Sleep(100 * time.Millisecond)
	if _, _, n := h.events.get(id); n != 1 {
		t.Fatalf("%d idle edges, want 1", n)
	}
	if !drainIdle(t, h) {
		t.Fatal("the supervisor cannot drain")
	}
}

// A decision delivered while the spool is down, after which claude keeps
// going (its output cannot be spooled either, so the child is stopped) or
// exits, settles durably once the spool is restored: the reply, the error
// status and the idle edge are all recorded, without a second decision.
func TestSpoolOutageAcrossContinuationAndExitSettles(t *testing.T) {
	for name, after := range map[string]string{
		"continue": `{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_a","model":"haiku","content":[{"type":"text","text":"AFTER"}]}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}`,
		"exit": `{"direction":"wait","message":%q}`,
	} {
		t.Run(name, func(t *testing.T) {
			fastRetries(t)
			gate := filepath.Join(t.TempDir(), "gate") // never opened: the child ends only by exit or kill
			h, got := promptHarness(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
`+fmt.Sprintf(after, gate)+"\n"), nil)
			id := h.create(t)
			h.send(t, id, "go")
			var p []platforms.LivePrompt
			waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
			restore := h.spoolDown(t, id)
			if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}); err == nil {
				t.Fatal("an unrecorded reply was acknowledged")
			}
			if name == "exit" {
				// Claude read the decision, then exits.
				waitFor(t, "decision read", func() bool { return strings.Contains(stdinLog(t), `"control_response"`) })
				h.live.mu.Lock()
				proc := h.live.procs[id]
				h.live.mu.Unlock()
				_ = proc.cmd.Process.Kill()
			}
			holdOutage(t, h, id)
			restore()
			expectSettled(t, h, id)
			if r := got.props("permission.replied"); len(r) != 1 || r[0]["reply"] != "once" {
				t.Fatalf("replied = %+v", r)
			}
			if n := strings.Count(stdinLog(t), `"control_response"`); n != 1 {
				t.Fatalf("claude received %d decisions", n)
			}
		})
	}
}

// A stopped live retires its durable-publication retry workers instead of
// leaving them sleeping forever against a spool path that will never work
// again (a removed test spool, or a provider shutdown).
func TestStopRetiresRetryWorkers(t *testing.T) {
	fastRetries(t)
	gate := filepath.Join(t.TempDir(), "gate") // never opened: the child ends only by kill
	h, _ := promptHarness(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"wait","message":`+fmt.Sprintf("%q", gate)+`}
`), nil)
	id := h.create(t)
	h.send(t, id, "go")
	var p []platforms.LivePrompt
	waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	restore := h.spoolDown(t, id)
	defer restore()
	if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}); err == nil {
		t.Fatal("an unrecorded reply was acknowledged")
	}
	h.live.mu.Lock()
	proc := h.live.procs[id]
	h.live.mu.Unlock()
	_ = proc.cmd.Process.Kill()
	holdOutage(t, h, id)
	// Both workers are retrying what the down spool cannot take.
	waitFor(t, "settle worker running", func() bool { h.live.mu.Lock(); defer h.live.mu.Unlock(); return h.live.settling[id] })
	waitFor(t, "record worker running", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		pr := h.live.prompts[p[0]["id"].(string)]
		return pr != nil && pr.retrying
	})
	h.live.stopAll()
	waitFor(t, "settle worker retired", func() bool { h.live.mu.Lock(); defer h.live.mu.Unlock(); return !h.live.settling[id] })
	waitFor(t, "record worker retired", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		pr := h.live.prompts[p[0]["id"].(string)]
		return pr != nil && !pr.retrying
	})
}

// A prompt claude withdraws (interrupt) or abandons (exit) while the spool
// is down stays pending, unanswerable and owned until its cancellation is
// recorded; the turn then settles and the supervisor drains.
func TestCancellationDuringOutageIsRecorded(t *testing.T) {
	for name, end := range map[string]string{
		"interrupt": `{"direction":"stdout","message":{"type":"control_cancel_request","request_id":"r1"}}
{"direction":"stdout","message":{"type":"result","subtype":"error_during_execution","is_error":true}}`,
		"exit": ``,
	} {
		t.Run(name, func(t *testing.T) {
			fastRetries(t)
			gate := filepath.Join(t.TempDir(), "gate")
			h, got := promptHarness(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"wait","message":%q}
%s
`, gate, end)), nil)
			id := h.create(t)
			h.send(t, id, "go")
			var p []platforms.LivePrompt
			waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
			restore := h.spoolDown(t, id)
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			holdOutage(t, h, id)
			h.live.mu.Lock()
			held := len(h.live.prompts)
			h.live.mu.Unlock()
			if held != 1 {
				t.Fatal("the cancelled prompt was dropped before its cancellation was recorded")
			}
			if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}); !errors.Is(err, platforms.ErrNotFound) {
				t.Fatalf("answering a cancelled prompt = %v", err)
			}
			restore()
			expectSettled(t, h, id)
			if r := got.props("permission.replied"); len(r) != 1 || r[0]["reason"] != "cancelled" {
				t.Fatalf("replied = %+v", r)
			}
			if strings.Contains(stdinLog(t), `"control_response"`) {
				t.Fatal("a decision reached claude for a cancelled prompt")
			}
		})
	}
}

// gatedStdin holds a control_response write until released, like a pipe
// claude is slow to read.
type gatedStdin struct {
	io.WriteCloser
	entered, release chan struct{}
}

func (g gatedStdin) Write(b []byte) (int, error) {
	if strings.Contains(string(b), `"control_response"`) {
		close(g.entered)
		<-g.release
	}
	return g.WriteCloser.Write(b)
}

// Claude cancelling a prompt while its decision is being written resolves it
// exactly once, as cancelled: the prompt is not dropped mid-write, the
// caller learns the decision did not take effect, and nothing else is sent.
func TestCancelDuringReplyWriteResolvesOnce(t *testing.T) {
	fastRetries(t)
	gate := filepath.Join(t.TempDir(), "gate")
	h, got := promptHarness(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"result","subtype":"error_during_execution","is_error":true}}
`, gate)), nil)
	id := h.create(t)
	h.send(t, id, "go")
	var p []platforms.LivePrompt
	waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	h.live.mu.Lock()
	proc := h.live.procs[id]
	g := gatedStdin{WriteCloser: proc.stdin, entered: make(chan struct{}), release: make(chan struct{})}
	proc.stdin = g
	h.live.mu.Unlock()
	replied := make(chan error, 1)
	go func() {
		replied <- h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"})
	}()
	<-g.entered
	h.live.cancel(proc, "r1") // claude's control_cancel_request
	h.live.mu.Lock()
	held := len(h.live.prompts)
	h.live.mu.Unlock()
	if held != 1 {
		t.Fatal("a prompt being answered was dropped by its cancellation")
	}
	close(g.release)
	if err := <-replied; !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("reply = %v, want the cancelled prompt's not-found", err)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if r := got.props("permission.replied"); len(r) != 1 || r[0]["reason"] != "cancelled" {
		t.Fatalf("replied = %+v, want one cancellation", r)
	}
	if !drainIdle(t, h) {
		t.Fatal("the supervisor cannot drain")
	}
}

// forwardThenHold passes a control_response to claude at once but holds the
// write's return, so claude can act on it and exit first.
type forwardThenHold struct {
	io.WriteCloser
	forwarded, release chan struct{}
}

func (g forwardThenHold) Write(b []byte) (int, error) {
	n, err := g.WriteCloser.Write(b)
	if strings.Contains(string(b), `"control_response"`) {
		close(g.forwarded)
		<-g.release
	}
	return n, err
}

// Claude receives an "Allow always", uses it and exits before the write
// returns. The exit cleanup is not a withdrawal: the decision claude got is
// what is recorded and reported, and the Allow-always grant is kept.
func TestDecisionDeliveredBeforeExitStands(t *testing.T) {
	fastRetries(t)
	h, got := promptHarness(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"exit","message":0}
`), nil)
	id := h.create(t)
	h.send(t, id, "go")
	var p []platforms.LivePrompt
	waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	h.live.mu.Lock()
	proc := h.live.procs[id]
	g := forwardThenHold{WriteCloser: proc.stdin, forwarded: make(chan struct{}), release: make(chan struct{})}
	proc.stdin = g
	h.live.mu.Unlock()
	replied := make(chan error, 1)
	go func() {
		replied <- h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "always"})
	}()
	<-g.forwarded
	waitFor(t, "claude consumed the decision and exited", func() bool {
		return strings.Contains(stdinLog(t), `"control_response"`) && h.procs() == 0
	})
	close(g.release)
	if err := <-replied; err != nil {
		t.Fatalf("the delivered decision was reported as %v", err)
	}
	waitFor(t, "turn idle", func() bool { _, _, n := h.events.get(id); return n == 1 })
	waitFor(t, "reply recorded", func() bool { return len(got.props("permission.replied")) != 0 })
	if r := got.props("permission.replied"); len(r) != 1 || r[0]["reply"] != "always" || r[0]["reason"] != nil {
		t.Fatalf("replied = %+v, want the delivered always", r)
	}
	if must(provider.OpenRules(filepath.Join(h.dir, "rules.json"))).Decide(platforms.LivePrompt{"sessionID": id, "permission": "bash", "patterns": []any{"make"}}) != "once" {
		t.Fatal("the delivered Allow always was dropped")
	}
	if !drainIdle(t, h) {
		t.Fatal("the supervisor cannot drain")
	}
}
