package claudeprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
	"github.com/NoUseFreak/ocman/internal/platforms/provider"
	"github.com/NoUseFreak/ocman/internal/providerprotocol"
)

// writeFixture writes a synthetic fake-claude fixture (the spike recorded no
// failing, crashing, queued-input or rejected turns).
func writeFixture(t *testing.T, rows string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.jsonl")
	if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (h *liveHarness) send(t *testing.T, id, text string) {
	t.Helper()
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: text}); err != nil {
		t.Fatal(err)
	}
}

func (h *liveHarness) visible(id, text string) bool {
	for _, e := range h.live.snapshot(id) {
		if strings.Contains(string(e.Properties), text) {
			return true
		}
	}
	return false
}

// settledAs checks every read path a routine, the queue and the list use.
func settledAs(t *testing.T, a *provider.Adapter, id string, want db.SessionStatus) {
	t.Helper()
	if life, err := a.SessionLifecycle(t.Context(), id); err != nil || life.Status != want {
		t.Fatalf("lifecycle = %+v %v, want %s", life, err, want)
	}
	if d, err := a.Session(t.Context(), id, 1, 0); err != nil || d.Session.Status != want {
		t.Fatalf("detail = %+v %v, want %s", d, err, want)
	}
}

// A failed result, a crashed child and a launch error all end the turn as
// error: a routine reading the session records a failure, never success.
func TestFailedTurnsSettleAsError(t *testing.T) {
	for name, rows := range map[string]string{
		"error result": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded"}}
`,
		"error subtype": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"error_max_turns","is_error":false,"errors":["API Error: 529 overloaded"]}}
`,
		"crash": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_a","model":"haiku","content":[{"type":"text","text":"working"}]}}}
{"direction":"exit","message":4}
`,
	} {
		t.Run(name, func(t *testing.T) {
			h := startLive(t, writeFixture(t, rows), nil)
			id := h.create(t)
			h.send(t, id, "hi")
			waitFor(t, "failed turn idle", func() bool { s, _, idles := h.events.get(id); return idles == 1 && s == db.StatusError })
			settledAs(t, h.adapter, id, db.StatusError)
			if name != "crash" && !h.visible(id, "API Error: 529 overloaded") {
				t.Fatalf("failure not visible: %+v", h.live.snapshot(id))
			}
		})
	}
}

// Input written while a turn runs is queued by the CLI (spike §3). Whether it
// joins the running loop (one result) or runs after it (two results), the
// session stays busy until every accepted input has finished: one idle edge.
func TestMidTurnInputKeepsTurnOpen(t *testing.T) {
	for name, rows := range map[string]string{
		"own result": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_1","model":"haiku","content":[{"type":"text","text":"FIRST"}]}}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"FIRST"}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:2","message":{"role":"user","content":"second"}}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_2","model":"haiku","content":[{"type":"text","text":"SECOND"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"SECOND"}}
`,
		"joined": `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_1","model":"haiku","content":[{"type":"text","text":"FIRST"}]}}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:2","message":{"role":"user","content":"second"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_2","model":"haiku","content":[{"type":"text","text":"SECOND"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"FIRST SECOND"}}
`,
	} {
		t.Run(name, func(t *testing.T) {
			gate := filepath.Join(t.TempDir(), "gate")
			stdin := filepath.Join(t.TempDir(), "stdin")
			t.Setenv("FAKE_CLAUDE_STDIN", stdin)
			h := startLive(t, writeFixture(t, fmt.Sprintf(rows, gate)), nil)
			id := h.create(t)
			h.send(t, id, "first")
			waitFor(t, "first reply", func() bool { return h.visible(id, "FIRST") })
			h.send(t, id, "second")
			// The fake reads the second input, then stops at the gate. An idle
			// edge here would be false: the second input is still outstanding.
			waitFor(t, "second input written", func() bool { return strings.Contains(string(must(os.ReadFile(stdin))), "second") })
			time.Sleep(300 * time.Millisecond)
			if s, _, idles := h.events.get(id); idles != 0 || s != db.StatusBusy {
				t.Fatalf("outstanding input settled the turn: status=%s idles=%d", s, idles)
			}
			h.live.mu.Lock()
			busy := h.live.procs[id] != nil && h.live.procs[id].busy
			h.live.mu.Unlock()
			if !busy {
				t.Fatal("a child with outstanding input must stay busy (never evicted or idled out)")
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
			if !h.visible(id, "SECOND") {
				t.Fatal("the idle edge came before the second input finished")
			}
			time.Sleep(100 * time.Millisecond)
			if s, _, idles := h.events.get(id); idles != 1 || s != db.StatusDone {
				t.Fatalf("status=%s idles=%d, want one idle edge", s, idles)
			}
			// Inputs reach the CLI in the order they were sent.
			log := string(must(os.ReadFile(stdin)))
			if strings.Index(log, "first") > strings.Index(log, "second") {
				t.Fatalf("stdin order: %s", log)
			}
		})
	}
}

// Aborting a turn still waiting for a pool slot ends it; it never starts later.
func TestAbortWhileWaitingForSlot(t *testing.T) {
	hold := filepath.Join(t.TempDir(), "hold")
	t.Setenv("FAKE_CLAUDE_HOLD", hold)
	runs := filepath.Join(t.TempDir(), "runs")
	t.Setenv("FAKE_CLAUDE_CWD_LOG", runs)
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.MaxProcs = 1 })
	a, b := h.create(t), h.create(t)
	h.send(t, a, "hi")
	waitFor(t, "first child running", func() bool { return h.procs() == 1 })
	h.send(t, b, "hi")
	waitFor(t, "second turn busy", func() bool { s, _, _ := h.events.get(b); return s == db.StatusBusy })
	if err := h.adapter.Abort(t.Context(), platforms.AbortRequest{SessionID: b}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aborted turn idle", func() bool { _, _, idles := h.events.get(b); return idles == 1 })
	if err := os.WriteFile(hold, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first turn idle", func() bool { _, _, idles := h.events.get(a); return idles == 1 })
	time.Sleep(300 * time.Millisecond)
	h.live.mu.Lock()
	_, started := h.live.procs[b]
	h.live.mu.Unlock()
	if started || strings.Count(string(must(os.ReadFile(runs))), "\n") != 1 {
		t.Fatalf("aborted turn started later: started=%v runs=%q", started, must(os.ReadFile(runs)))
	}
	settledAs(t, h.adapter, b, db.StatusDone)
}

func drainIdle(t *testing.T, h *liveHarness) bool {
	t.Helper()
	rec := httptest.NewRecorder()
	h.live.sup.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/drain", nil))
	var state providerprotocol.DrainState
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	return state.Idle
}

// A rejected rate limit keeps the session limited, not idle, after its turn
// ends: no idle edge (queue drain, routine, plugin reply) and no supervisor
// drain until the reset time, across an ocman restart. At the reset the turn
// settles as failed and the idle edge is delivered.
func TestUsageLimitHoldsUntilReset(t *testing.T) {
	h := startLive(t, "", nil)
	id := h.create(t)
	// Start the reset clock after CLI setup, not while building/probing it.
	resets := time.Now().Unix() + 2
	t.Setenv("FAKE_CLAUDE_FIXTURE", writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":%d,"rateLimitType":"five_hour"}}}
{"direction":"stdout","message":{"type":"result","is_error":true,"subtype":"success","result":"Claude usage limit reached"}}
`, resets)))
	h.send(t, id, "hi")
	waitFor(t, "limited", func() bool { s, at, _ := h.events.get(id); return s == db.StatusLimited && at == resets*1000 })
	waitFor(t, "result handled", func() bool { return h.visible(id, "usage limit reached") })

	// ocman restarts: a fresh adapter replays the unacknowledged turn.
	h.adapter.Close()
	restarted, err := provider.New(t.Context(), filepath.Join(h.dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	idled := make(chan struct{}, 4)
	restarted.SetSessionEventSink(platforms.SessionEventSink{OnSessionIdle: func(_, sid string) {
		if sid == id {
			idled <- struct{}{}
		}
	}})
	go func() { _ = restarted.WatchEvents(t.Context()) }()
	waitFor(t, "limited after restart", func() bool {
		s := restarted.LiveStatus(id)
		return s != nil && s.Status == string(db.StatusLimited)
	})
	settledAs(t, restarted, id, db.StatusLimited)
	if _, _, idles := h.events.get(id); idles != 0 || len(idled) != 0 {
		t.Fatal("a limited turn delivered an idle edge before its reset")
	}
	if drainIdle(t, h) {
		t.Fatal("a limited session let the supervisor drain")
	}

	select {
	case <-idled:
	case <-time.After(10 * time.Second):
		t.Fatal("no idle edge after the reset time")
	}
	if time.Now().Unix() < resets {
		t.Fatal("idle edge before the reset time")
	}
	settledAs(t, restarted, id, db.StatusError)
	if !drainIdle(t, h) {
		t.Fatal("the supervisor must drain once the limit has reset")
	}
}
