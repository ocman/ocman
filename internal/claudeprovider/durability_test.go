package claudeprovider

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/claudeprovider/claudetest"
	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// setHook installs testHook for one test.
func setHook(t *testing.T, hook func(point, id string)) {
	t.Helper()
	old := testHooks.Swap(&hook)
	t.Cleanup(func() { testHooks.Store(old) })
}

// A session whose ownership cannot be recorded is not created: the error
// reaches the caller, nothing half-written survives a restart, and earlier
// sessions stay owned. Ownership is never written without its lock.
func TestOwnershipFailureIsVisible(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	state := filepath.Join(h.dir, "owned.json")
	kept := h.create(t)
	for name, inject := range map[string]string{"write": state + ".tmp", "lock": state + ".lock"} {
		t.Run(name, func(t *testing.T) {
			_ = os.Remove(inject)
			if err := os.Mkdir(inject, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(inject, "x"), nil, 0o600); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = os.RemoveAll(inject) }()
			if _, err := h.adapter.CreateSession(t.Context(), platforms.CreateSessionRequest{Directory: h.dir}); err == nil {
				t.Fatal("create acknowledged an unrecorded session")
			}
			if err := h.adapter.RenameSession(t.Context(), platforms.RenameSessionRequest{SessionID: kept, Title: "renamed"}); err == nil {
				t.Fatal("rename acknowledged an unrecorded title")
			}
			sessions, err := h.adapter.Sessions(t.Context(), h.dir, 0)
			if err != nil || len(sessions) != 1 || sessions[0].ID != kept || sessions[0].Title != "fresh" {
				t.Fatalf("sessions after failed writes = %+v %v", sessions, err)
			}
		})
	}
	// A restarted supervisor reads exactly what was acknowledged.
	if _, ok := OwnedDirectory(state, kept); !ok {
		t.Fatal("an acknowledged session was lost")
	}
	if all := string(must(os.ReadFile(state))); strings.Count(all, `"directory"`) != 1 {
		t.Fatalf("ownership on disk = %s", all)
	}
}

// An event that cannot be spooled is never silently dropped: the turn fails
// visibly, the child (and its unseen prompt) is stopped, and a new send is
// refused while the spool cannot record it.
func TestSpoolFailureFailsTurnVisibly(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","input":{"command":"make"},"tool_use_id":"t1"}}}
{"direction":"stdin","message":{"type":"control_response","response":{"subtype":"success","request_id":"r1"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"done"}}
`, gate)), nil)
	id := h.create(t)
	h.send(t, id, "hi")
	waitFor(t, "child running", func() bool { return h.procs() == 1 })
	h.spoolDown(t, id)
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "child stopped", func() bool { return h.procs() == 0 })
	if p := h.live.pendingPrompts(id, false); len(p) != 0 {
		t.Fatalf("an unspooled prompt stayed pending: %+v", p)
	}
	if life, err := h.adapter.SessionLifecycle(t.Context(), id); err != nil || life.Status != db.StatusError {
		t.Fatalf("lifecycle = %+v %v, want error", life, err)
	}
	if err := h.adapter.SendMessage(t.Context(), platforms.SendMessageRequest{SessionID: id, Message: "again"}); err == nil {
		t.Fatal("a send was accepted without a durable busy event")
	}
}

const twoTurns = `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_1","model":"haiku","content":[{"type":"text","text":"FIRST"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"FIRST"}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:2","message":{"role":"user","content":"second"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_2","model":"haiku","content":[{"type":"text","text":"SECOND"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"SECOND"}}
`

// expectOneIdleAfter checks that no idle edge arrives while the second turn
// is held, and exactly one once it finishes.
func expectOneIdleAfter(t *testing.T, h *liveHarness, id, gate string) {
	t.Helper()
	waitFor(t, "second input started", func() bool { return h.visible(id, "second") || h.gen(id) == 2 })
	time.Sleep(300 * time.Millisecond)
	if s, _, idles := h.events.get(id); idles != 0 || s != db.StatusBusy {
		t.Fatalf("stale idle edge with an accepted turn outstanding: status=%s idles=%d", s, idles)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "second turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	if !h.visible(id, "SECOND") {
		t.Fatal("idle before the second turn finished")
	}
	time.Sleep(100 * time.Millisecond)
	if s, _, idles := h.events.get(id); idles != 1 || s != db.StatusDone {
		t.Fatalf("status=%s idles=%d, want one idle edge", s, idles)
	}
}

func (h *liveHarness) gen(id string) uint64 {
	h.live.mu.Lock()
	defer h.live.mu.Unlock()
	return h.live.accepted[id]
}

// A send accepted after a result decided its turn was over, but before the
// idle edge was published, keeps the session busy: the old edge is dropped.
func TestSendRacingResultKeepsTurnOpen(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, writeFixture(t, fmt.Sprintf(twoTurns, gate)), nil)
	id := h.create(t)
	var once sync.Once
	setHook(t, func(point, sid string) {
		if point == "settle" && sid == id {
			once.Do(func() { h.send(t, id, "second") })
		}
	})
	h.send(t, id, "first")
	expectOneIdleAfter(t, h, id, gate)
}

// A send accepted before the previous result, but not yet written to the
// child when that result arrives, is outstanding work: no idle in between.
func TestAcceptedUnwrittenSendKeepsTurnOpen(t *testing.T) {
	gate, first := filepath.Join(t.TempDir(), "gate"), filepath.Join(t.TempDir(), "first")
	// The first result waits for its own gate, so it arrives after the second send.
	rows := strings.Replace(twoTurns, `{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"FIRST"}}`,
		fmt.Sprintf(`{"direction":"wait","message":%q}`, first)+"\n"+`{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"FIRST"}}`, 1)
	h := startLive(t, writeFixture(t, fmt.Sprintf(rows, gate)), func(o *Options) { o.IdleTimeout = time.Hour })
	id := h.create(t)
	release := make(chan struct{})
	var calls int
	var mu sync.Mutex
	setHook(t, func(point, sid string) {
		if point != "acquire" || sid != id {
			return
		}
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		if n == 2 {
			<-release // the second send waits to be written
		}
	})
	h.send(t, id, "first")
	waitFor(t, "first reply", func() bool { return h.visible(id, "FIRST") })
	h.send(t, id, "second")
	if err := os.WriteFile(first, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first result handled", func() bool {
		h.live.mu.Lock()
		defer h.live.mu.Unlock()
		p := h.live.procs[id]
		return p != nil && !p.busy
	})
	time.Sleep(300 * time.Millisecond)
	if _, _, idles := h.events.get(id); idles != 0 {
		t.Fatal("idle edge while an accepted send was not yet written")
	}
	close(release)
	expectOneIdleAfter(t, h, id, gate)
}

// A send landing while a rejected turn settles owns the idle edge.
func TestSendRacingUsageLimitResultKeepsTurnOpen(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, "", nil)
	id := h.create(t)
	var once sync.Once
	setHook(t, func(point, sid string) {
		if point == "settle" && sid == id {
			once.Do(func() { h.send(t, id, "second") })
		}
	})
	resets := time.Now().Unix() + 3600
	t.Setenv("FAKE_CLAUDE_FIXTURE", writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":%d,"rateLimitType":"five_hour"}}}
{"direction":"stdout","message":{"type":"result","is_error":true,"subtype":"success","result":"Claude usage limit reached"}}
{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:2","message":{"role":"user","content":"second"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_2","model":"haiku","content":[{"type":"text","text":"SECOND"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"SECOND"}}
`, resets, gate)))
	h.send(t, id, "first")
	waitFor(t, "second turn accepted", func() bool { return h.gen(id) == 2 })
	expectOneIdleAfter(t, h, id, gate)
}

// The machine cap counts live processes: an evicted child that is slow to
// exit keeps its slot until it is reaped, so no more than MaxProcs claude
// children ever run at once.
func TestSlowExitingChildHoldsItsSlot(t *testing.T) {
	for name, slots := range map[string]bool{"process": false, "machine": true} {
		t.Run(name, func(t *testing.T) {
			life := filepath.Join(t.TempDir(), "life")
			t.Setenv("FAKE_CLAUDE_LIFE_LOG", life)
			t.Setenv("FAKE_CLAUDE_EXIT_DELAY", "700ms")
			h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) {
				o.MaxProcs = 1
				if slots {
					o.SlotDir = filepath.Join(t.TempDir(), "slots")
				}
			})
			a, b := h.create(t), h.create(t)
			for _, id := range []string{a, b} {
				h.send(t, id, "hi")
				waitFor(t, "turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
			}
			h.live.stopAll()
			waitFor(t, "children reaped", func() bool {
				return strings.Count(string(must(os.ReadFile(life))), "exit") == 2
			})
			type mark struct {
				at    int64
				delta int
			}
			var marks []mark
			for _, line := range strings.Split(strings.TrimSpace(string(must(os.ReadFile(life)))), "\n") {
				kind, at, _ := strings.Cut(line, " ")
				n, _ := strconv.ParseInt(at, 10, 64)
				marks = append(marks, mark{n, map[string]int{"start": 1, "exit": -1}[kind]})
			}
			sort.Slice(marks, func(i, j int) bool { return marks[i].at < marks[j].at })
			running := 0
			for _, m := range marks {
				if running += m.delta; running > 1 {
					t.Fatalf("%d claude children alive at once with a cap of 1: %s", running, must(os.ReadFile(life)))
				}
			}
		})
	}
}

// A successful turn whose only output is its result (a local command) shows
// that text once; a normal reply is not duplicated.
func TestResultOnlyOutputIsShown(t *testing.T) {
	h := startLive(t, writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:1","message":{"role":"user","content":"/cost"}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"Total cost: $0.0042"}}
`), nil)
	id := h.create(t)
	h.send(t, id, "/cost")
	waitFor(t, "idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	if !h.visible(id, "Total cost: $0.0042") {
		t.Fatalf("result-only output missing: %+v", h.live.snapshot(id))
	}

	plain := startLive(t, claudetest.Fixture("plain.jsonl"), nil)
	pid := plain.create(t)
	plain.send(t, pid, "hi")
	waitFor(t, "idle", func() bool { _, _, idles := plain.events.get(pid); return idles == 1 })
	if plain.visible(pid, "msg_res_") || !plain.visible(pid, "SPIKE_OK") {
		t.Fatalf("normal reply duplicated or lost: %+v", plain.live.snapshot(pid))
	}
}

// The process's settled status wins over transcript inference: an aborted
// turn whose transcript ends mid-turn reads done, not interrupted.
func TestProcessStatusOverridesInference(t *testing.T) {
	l := newLive(nil, Options{}, claudetest.Version)
	l.status["s"] = db.Session{Status: db.StatusDone}
	l.status["failed"] = db.Session{Status: db.StatusError}
	s := db.Session{ID: "s", Status: db.StatusInterrupted}
	l.overlay(&s)
	failed := db.Session{ID: "failed", Status: db.StatusDone}
	l.overlay(&failed)
	if s.Status != db.StatusDone || failed.Status != db.StatusError {
		t.Fatalf("overlay = %+v %+v", s, failed)
	}
}

// A result read before its own input's wait ticket is removed (the reader
// beat the sender's cleanup) still ends the turn: exactly one idle edge, so
// the queue and routines are never stranded on a stale busy.
func TestResultBeforeTicketRemovalSettles(t *testing.T) {
	h := startLive(t, claudetest.Fixture("plain.jsonl"), func(o *Options) { o.IdleTimeout = time.Hour })
	id := h.create(t)
	setHook(t, func(point, sid string) {
		if point != "written" || sid != id {
			return
		}
		waitFor(t, "result handled", func() bool {
			h.live.mu.Lock()
			defer h.live.mu.Unlock()
			p := h.live.procs[id]
			return p != nil && !p.busy
		})
		time.Sleep(100 * time.Millisecond) // the suppressed settle has run
	})
	h.send(t, id, "hi")
	waitFor(t, "idle edge", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	time.Sleep(200 * time.Millisecond)
	if s, _, idles := h.events.get(id); idles != 1 || s != db.StatusDone {
		t.Fatalf("status=%s idles=%d, want exactly one idle edge", s, idles)
	}
	if life, err := h.adapter.SessionLifecycle(t.Context(), id); err != nil || life.Status != db.StatusDone {
		t.Fatalf("lifecycle = %+v %v, want done", life, err)
	}
}

// A browser that read busy over REST, then attached after the turn ended,
// gets the terminal state from the snapshot: the idle edge it missed lies
// before the snapshot's cursor, so the snapshot must carry it.
func TestSnapshotCarriesTerminalState(t *testing.T) {
	for name, fixture := range map[string]string{
		"done": claudetest.Fixture("plain.jsonl"),
		"error": writeFixture(t, `{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":true,"result":"API Error: 529 overloaded"}}
`),
	} {
		t.Run(name, func(t *testing.T) {
			// Keep the fake result behind the REST read, not the scheduler.
			gate := filepath.Join(t.TempDir(), "result")
			t.Setenv("FAKE_CLAUDE_HOLD", gate)
			h := startLive(t, fixture, func(o *Options) { o.IdleTimeout = time.Hour })
			id := h.create(t)
			h.send(t, id, "hi")
			if life, err := h.adapter.SessionLifecycle(t.Context(), id); err != nil || life.Status != db.StatusBusy {
				t.Fatalf("REST read = %+v %v, want busy", life, err)
			}
			if err := os.WriteFile(gate, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			waitFor(t, "turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
			var buf bytes.Buffer
			var mu sync.Mutex
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			go func() { _ = h.adapter.ProxyEvents(ctx, id, lockedWriter{&mu, &buf}, func() {}) }()
			waitFor(t, "terminal snapshot", func() bool {
				mu.Lock()
				defer mu.Unlock()
				return strings.Contains(buf.String(), `"type":"session.idle"`)
			})
			mu.Lock()
			defer mu.Unlock()
			if name == "error" && !strings.Contains(buf.String(), `"type":"error"`) {
				t.Fatalf("snapshot lost the failed status: %s", buf.String())
			}
		})
	}
}

// A replay echo whose ownership update fails latches the turn as failed: it
// never settles as success, even if storage recovers before the result, and
// the session (which has a transcript now) is never relaunched as a fresh
// --session-id: the next launch records ownership first, then resumes.
func TestStartedPersistenceFailureFailsTurn(t *testing.T) {
	gate := filepath.Join(t.TempDir(), "gate")
	h := startLive(t, writeFixture(t, fmt.Sprintf(`{"direction":"stdin","message":{"type":"user"}}
{"direction":"stdout","message":{"type":"user","isReplay":true,"uuid":"$input:1","message":{"role":"user","content":"hi"}}}
{"direction":"wait","message":%q}
{"direction":"stdout","message":{"type":"assistant","message":{"id":"msg_1","model":"haiku","content":[{"type":"text","text":"OK"}]}}}
{"direction":"stdout","message":{"type":"result","subtype":"success","is_error":false,"result":"OK"}}
`, gate)), func(o *Options) { o.IdleTimeout = time.Hour })
	id := h.create(t)
	inject := filepath.Join(h.dir, "owned.json.tmp")
	if err := os.MkdirAll(filepath.Join(inject, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	h.send(t, id, "hi")
	waitFor(t, "echo handled", func() bool { return h.visible(id, "owned.json") })
	if err := os.RemoveAll(inject); err != nil { // storage recovers before the result
		t.Fatal(err)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "turn settled", func() bool { _, _, idles := h.events.get(id); return idles == 1 })
	if s, _, _ := h.events.get(id); s != db.StatusError {
		t.Fatalf("status = %s, want error after a failed ownership update", s)
	}
	waitFor(t, "child stopped", func() bool { return h.procs() == 0 })
	h.send(t, id, "again")
	waitFor(t, "second turn idle", func() bool { _, _, idles := h.events.get(id); return idles == 2 })
	if args := h.launchArgs(t); !slices.Contains(args, "--resume") || slices.Contains(args, "--session-id") {
		t.Fatalf("relaunch args = %q, want --resume", args)
	}
}
