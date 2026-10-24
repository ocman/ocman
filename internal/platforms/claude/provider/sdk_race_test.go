package claudeprovider

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// An input admitted while an interrupt awaits its acknowledgment was not
// part of that interrupt: the acknowledgment must not erase it, so the
// interrupted result cannot publish idle while it is still outstanding.
func TestSDKInterruptKeepsInputAdmittedAfterBoundary(t *testing.T) {
	l, p := sdkLifecycle(t)
	p.done, p.controls, p.stderr = make(chan struct{}), map[string]chan controlResult{}, &tail{}
	p.pending["old"] = true
	p.stdin = sdkWriter{func(b []byte) (int, error) {
		var request struct {
			ID string `json:"request_id"`
		}
		_ = json.Unmarshal(b, &request)
		l.mu.Lock()
		p.pending["new"] = true // a concurrent send joins the open turn
		l.mu.Unlock()
		l.receiveControl(p, []byte(`{"response":{"subtype":"success","request_id":"`+request.ID+`","response":{"still_queued":[]}}}`))
		return len(b), nil
	}}
	if err := l.abort(t.Context(), p.id); err != nil {
		t.Fatal(err)
	}
	l.result(p, []byte(`{"subtype":"error_during_execution","is_error":true}`))
	if !p.busy || !p.pending["new"] || p.pending["old"] {
		t.Fatalf("busy=%v pending=%v: the newer input was erased by the earlier interrupt", p.busy, p.pending)
	}
	l.started(p, "new")
	l.result(p, []byte(`{"subtype":"success"}`))
	if p.busy || l.status[p.id].Status != db.StatusDone {
		t.Fatal("the newer input's own result did not settle the turn")
	}
}

// An input whose turn has not been written when the interrupt is sent is
// cancelled by the abort, not covered by its acknowledgment.
func TestSDKInterruptLeavesUnwrittenInputToItsTurn(t *testing.T) {
	l, p := sdkLifecycle(t)
	p.done, p.controls, p.stderr = make(chan struct{}), map[string]chan controlResult{}, &tail{}
	p.pending["old"], p.pending["queued"] = true, true
	ticket := &waitTicket{input: "queued"}
	l.waits[p.id] = map[*waitTicket]bool{ticket: true}
	p.stdin = sdkWriter{func(b []byte) (int, error) {
		var request struct {
			ID string `json:"request_id"`
		}
		_ = json.Unmarshal(b, &request)
		l.receiveControl(p, []byte(`{"response":{"subtype":"success","request_id":"`+request.ID+`","response":{"still_queued":[]}}}`))
		return len(b), nil
	}}
	if err := l.abort(t.Context(), p.id); err != nil {
		t.Fatal(err)
	}
	if !ticket.cancelled || !p.pending["queued"] || p.pending["old"] {
		t.Fatalf("cancelled=%v pending=%v", ticket.cancelled, p.pending)
	}
}

// A child that exited is out of the reusable inventory before its deferred
// result publishes idle, so a follow-up launches a resumed child instead of
// writing to the closed one.
func TestSDKExitedChildIsNotReusedAfterDeferredIdle(t *testing.T) {
	h := startLive(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:1","message":{"role":"user","content":"x"}}}
{"direction":"stdout","message":{"type":"system","subtype":"session_state_changed","state":"running"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_1","model":"haiku","content":[{"type":"text","text":"OK"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"OK"}}
{"direction":"exit","message":0}
`), nil)
	id := h.create(t)
	reused := make(chan bool, 4)
	setHook(t, func(point, sid string) {
		if point == "settle" && sid == id {
			h.live.mu.Lock()
			p := h.live.procs[id]
			reused <- p != nil && func() bool {
				select {
				case <-p.done:
					return true
				default:
					return false
				}
			}()
			h.live.mu.Unlock()
		}
	})
	h.send(t, id, "first")
	waitFor(t, "deferred idle at exit", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if <-reused {
		t.Fatal("an exited child was still reusable when its idle edge was published")
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "follow-up"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "follow-up on a resumed child", func() bool { s, _, n := h.events.get(id); return n == 2 && s == db.StatusDone })
	if args := h.launchArgs(t); !slices.Contains(args, "--resume") {
		t.Fatalf("follow-up did not resume a fresh child: %q", args)
	}
}
