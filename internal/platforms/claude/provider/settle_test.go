package claudeprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/providerprotocol"

	"github.com/ocman/ocman/internal/platforms"
)

const oneTurn = `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`

// until polls cond for up to five seconds off the test goroutine.
func until(cond func() bool) bool {
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if cond() {
			return true
		}
	}
	return false
}

// The retry worker of turn A reads A, then turn B is accepted, completes and
// also cannot publish its end, replacing A's entry while A's worker runs. A's
// retry is superseded; the worker must go on to settle B, not exit and leave
// B (and /drain, the queue and routines) unsettled forever.
func TestSettlementRetryOwnsEveryGeneration(t *testing.T) {
	fastRetries(t)
	h, _ := promptHarness(t, writeFixture(t, oneTurn), nil)
	id := h.create(t)
	restores := make(chan func(), 2)
	var phase atomic.Int32
	failed := make(chan bool, 1)
	setHook(t, func(point, sid string) {
		if sid != id {
			return
		}
		switch {
		case point == "settle" && (phase.Load() == 0 || phase.Load() == 2):
			restores <- h.spoolDown(t, id) // this turn's end cannot be published
			phase.Add(1)
		case point == "retry" && phase.CompareAndSwap(1, 2):
			(<-restores)()
			ok := until(func() bool { return h.procs() == 0 }) &&
				h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "B"}) == nil &&
				until(func() bool { h.live.mu.Lock(); defer h.live.mu.Unlock(); return h.live.unsettled[id].gen == 2 })
			failed <- !ok
		}
	})
	h.send(t, id, "A")
	select {
	case bad := <-failed:
		if bad {
			t.Fatal("turn B was not accepted and left unsettled")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the retry worker never ran")
	}
	holdOutage(t, h, id)
	(<-restores)()
	expectSettled(t, h, id)
}

// reconnectHeadless opens and closes a headless stream at the supervisor's
// current cursor, as a reconnecting watcher does: pending prompts are
// re-delivered, and the stream's end prunes the spool.
func reconnectHeadless(t *testing.T, h *liveHarness, id string) {
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/events?consumer=headless&after="+strconv.FormatUint(cursor(t, h, id), 10), nil).WithContext(ctx)
	h.live.sup.ServeHTTP(httptest.NewRecorder(), req)
}

// cursor is the supervisor's latest sequence.
func cursor(t *testing.T, h *liveHarness, id string) uint64 {
	t.Helper()
	rec := httptest.NewRecorder()
	h.live.sup.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sessions/"+id+"/snapshot", nil))
	var snap providerprotocol.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err, rec.Body.String())
	}
	return snap.Cursor
}

// lastIdle is the sequence of the session's latest spooled idle edge.
func lastIdle(t *testing.T, h *liveHarness, id string) uint64 {
	t.Helper()
	var seq uint64
	for _, line := range strings.Split(string(must(os.ReadFile(h.spoolPath(id)))), "\n") {
		var rec struct {
			Sequence uint64                 `json:"sequence"`
			Event    providerprotocol.Event `json:"event"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Event.Type == "session.idle" {
			seq = rec.Sequence
		}
	}
	return seq
}

func (h *liveHarness) spoolPath(id string) string {
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(h.dir, "spool", hex.EncodeToString(sum[:])+".jsonl")
}

// A decision claude received during an outage is recorded only after the
// turn's idle edge was published and acknowledged and the headless watcher
// reconnected (re-delivering the still-pending prompt). The re-delivery must
// not reopen the finished turn (drain stays possible once it is recorded),
// and the spool keeps the ask with its late resolution, so a consumer
// replaying from the start (an ocman restart) still has the prompt metadata.
func TestLateResolutionAfterIdleAndReconnect(t *testing.T) {
	h, id, got := lateResolution(t, "")
	if !drainIdle(t, h) {
		t.Fatal("the re-delivered prompt reopened the finished turn")
	}
	reconnectHeadless(t, h, id) // prunes
	spool := string(must(os.ReadFile(h.spoolPath(id))))
	if !strings.Contains(spool, `"permission.asked"`) || !strings.Contains(spool, `"permission.replied"`) {
		t.Fatalf("the late resolution lost its ask: %s", spool)
	}
	if _, _, n := h.events.get(id); n != 1 {
		t.Fatalf("%d idle edges, want 1", n)
	}
	_ = got
}

const askedTurn = `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`

// A decision claude received during its turn must be spooled before the
// turn's idle edge. On a starved runner the record can lose to the result
// written right after the decision: the acknowledged idle edge then makes
// the spool keep the ask as a late resolution forever (pruneCut), and the
// spool never fully truncates. Holding the record through the settle closes
// that race window on demand; the spool must still truncate.
func TestRecordedPromptReplyCannotLoseToIdleEdge(t *testing.T) {
	h, got := promptHarness(t, writeFixture(t, askedTurn), func(h *liveHarness, e providerprotocol.Event) {
		if e.Type == "permission.asked" {
			var p platforms.LivePrompt
			_ = json.Unmarshal(e.Properties, &p)
			go func() {
				_ = h.adapter.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: p["sessionID"].(string), PermissionID: p["id"].(string), Reply: "once"})
			}()
		}
	})
	var once atomic.Bool
	setHook(t, func(point, sid string) {
		if point != "recording" || !once.CompareAndSwap(false, true) {
			return
		}
		// Hold the record while the turn settles without it: an idle edge
		// published in this window is the record losing. The hold ends with
		// the first edge, or once the settle has clearly been held back.
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if _, _, n := h.events.get(sid); n > 0 {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond) // an acknowledgment of that edge lands
	})
	id := h.create(t)
	h.turn(t, id, 1)
	waitFor(t, "spool truncated after acknowledgment", func() bool {
		info, err := os.Stat(h.spoolPath(id))
		return err == nil && info.Size() == 0
	})
	if n := len(got.props("permission.replied")); n != 1 {
		t.Fatalf("replied = %d, want 1", n)
	}
	if pending, _ := h.adapter.ListPermissions(t.Context(), id); len(pending) != 0 {
		t.Fatalf("prompt still pending: %+v", pending)
	}
	if s, _, n := h.events.get(id); n != 1 || s != db.StatusDone {
		t.Fatalf("status=%s idles=%d, want exactly one idle edge", s, n)
	}
}

// A settle held back for a record that then fails is not left to that
// record: once the record waits in retry backoff, the turn's end is
// published (or retried on its own) without it, as during any outage.
func TestDeferredSettleSurvivesFailedRecord(t *testing.T) {
	fastRetries(t)
	setRecordRetry(t, time.Hour) // the failed record stays in backoff
	h, _ := promptHarness(t, writeFixture(t, askedTurn), func(h *liveHarness, e providerprotocol.Event) {
		if e.Type == "permission.asked" {
			var p platforms.LivePrompt
			_ = json.Unmarshal(e.Properties, &p)
			go func() {
				_ = h.adapter.RespondPermission(context.Background(), platforms.RespondPermissionRequest{SessionID: p["sessionID"].(string), PermissionID: p["id"].(string), Reply: "once"})
			}()
		}
	})
	id := h.create(t)
	held, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	setHook(t, func(point, sid string) {
		if point == "recording" && sid == id && once.CompareAndSwap(false, true) {
			close(held)
			<-release
		}
	})
	h.send(t, id, "go")
	<-held
	waitFor(t, "settle deferred for the record", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		_, ok := h.live.deferred[id]
		return ok
	})
	restore := h.spoolDown(t, id)
	close(release) // the record fails and enters backoff
	waitFor(t, "record in backoff", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		for _, pr := range h.live.prompts {
			if pr.retrying {
				return true
			}
		}
		return false
	})
	restore()
	waitFor(t, "idle edge without the record", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if p := h.live.pendingPrompts(id, false); len(p) != 1 {
		t.Fatalf("the unrecorded prompt = %+v, want it still pending", p)
	}
}

// lateResolution runs turn A to the state TestLateResolutionAfterIdleAndReconnect
// describes: A's idle edge published and acknowledged, the watcher
// reconnected, then A's reply recorded and the supervisor released. A
// non-empty next replaces the fixture for the following child.
func lateResolution(t *testing.T, next string) (*liveHarness, string, *seen) {
	t.Helper()
	fastRetries(t)
	held, release := make(chan struct{}), make(chan struct{})
	var once atomic.Bool
	var target atomic.Value // this test's prompt: other tests' retries may still run
	target.Store("")
	setHook(t, func(point, pid string) {
		if point == "record" && pid == target.Load() && once.CompareAndSwap(false, true) {
			close(held)
			<-release
		}
	})
	fixturePath := writeFixture(t, askedTurn)
	h, got := promptHarness(t, fixturePath, nil)
	id := h.create(t)
	h.send(t, id, "go")
	var p []platforms.LivePrompt
	waitFor(t, "prompt", func() bool { p, _ = h.adapter.ListPermissions(t.Context(), id); return len(p) == 1 })
	target.Store(p[0]["id"].(string))
	restore := h.spoolDown(t, id)
	if err := h.adapter.RespondPermission(t.Context(), platforms.RespondPermissionRequest{SessionID: id, PermissionID: p[0]["id"].(string), Reply: "once"}); err == nil {
		t.Fatal("an unrecorded reply was acknowledged")
	}
	<-held // the resolution retry is held
	waitFor(t, "child gone", func() bool { return h.procs() == 0 })
	if next != "" {
		if err := os.WriteFile(fixturePath, []byte(next), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	restore()
	waitFor(t, "idle edge", func() bool { _, _, n := h.events.get(id); return n == 1 })
	// The watcher acknowledges the idle edge; acknowledging it again here
	// makes that ordering certain.
	ack := httptest.NewRecorder()
	h.live.sup.ServeHTTP(ack, httptest.NewRequest(http.MethodPost, "/sessions/"+id+"/ack", strings.NewReader(fmt.Sprintf(`{"sequence":%d}`, lastIdle(t, h, id)))))
	if ack.Code != http.StatusNoContent {
		t.Fatalf("ack = %d %s; spool: %s", ack.Code, ack.Body.String(), must(os.ReadFile(h.spoolPath(id))))
	}
	reconnectHeadless(t, h, id)
	close(release)
	waitFor(t, "resolution recorded", func() bool { return len(got.props("permission.replied")) == 1 })
	waitFor(t, "supervisor released", func() bool { return !h.supervisorActive(id) })
	return h, id, got
}

// Turn A's end cannot be published; before A's failure is handled the spool
// recovers and turn B is accepted and running. A's stale failure must not
// mark B failed or kill B's child.
func TestStaleTurnFailureSparesNextTurn(t *testing.T) {
	fastRetries(t)
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, writeFixture(t, fmt.Sprintf(twoTurns, gate)), func(o *Options) { o.IdleTimeout = time.Hour })
	id := h.create(t)
	var restore func()
	var phase atomic.Int32
	running := make(chan bool, 1)
	setHook(t, func(point, sid string) {
		if sid != id {
			return
		}
		switch {
		case point == "settle" && phase.CompareAndSwap(0, 1):
			restore = h.spoolDown(t, id)
		case point == "failed" && phase.CompareAndSwap(1, 2):
			restore()
			ok := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "second"}) == nil &&
				until(func() bool {
					h.live.mu.Lock()
					defer h.live.mu.Unlock()
					p := h.live.procs[id]
					return h.live.accepted[id] == 2 && p != nil && p.busy && len(h.live.waits[id]) == 0
				})
			running <- ok
		}
	})
	h.send(t, id, "first")
	select {
	case ok := <-running:
		if !ok {
			t.Fatal("turn B never started")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("A's failure was never handled")
	}
	time.Sleep(100 * time.Millisecond)
	h.live.mu.Lock()
	p := h.live.procs[id]
	alive, status := p != nil && p.busy, h.live.status[id].Status
	h.live.mu.Unlock()
	if !alive || status != db.StatusBusy {
		t.Fatalf("turn B: running=%v status=%s, want a running busy turn", alive, status)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "B's idle edge", func() bool { _, _, n := h.events.get(id); return n == 1 })
	if s, _, _ := h.events.get(id); s != db.StatusDone {
		t.Fatalf("status = %s, want B done", s)
	}
}
