package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/hostsvc"
	"github.com/ocman/ocman/internal/platforms"
)

func postMessage(t *testing.T, srv *Server, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/session/"+sessionID+"/message?platform=fake",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	serveSessionRoute(srv.handleSessionMessage, rr, req)
	return rr
}

// queueFake is the "fake" platform's view of session s1: it records sent
// messages and reports busy or done as toggled by setBusy.
type queueFake struct {
	mu   sync.Mutex
	busy bool
	sent []string
}

func newQueueFake(t *testing.T, busy bool) (*Server, *queueFake) {
	t.Helper()
	srv, reg := newSessionsTestServer(t)
	q := &queueFake{busy: busy}
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			q.mu.Lock()
			defer q.mu.Unlock()
			q.sent = append(q.sent, req.Message)
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			q.mu.Lock()
			defer q.mu.Unlock()
			status := db.StatusDone
			if q.busy {
				status = db.StatusBusy
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})
	return srv, q
}

func (q *queueFake) setBusy(busy bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.busy = busy
}

func (q *queueFake) sentMessages() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.sent)
}

func TestSessionMessage_IdleSendsImmediately(t *testing.T) {
	srv, q := newQueueFake(t, false)

	rr := postMessage(t, srv, "s1", `{"message":"hello"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	if sent := q.sentMessages(); len(sent) != 1 || sent[0] != "hello" {
		t.Fatalf("sent = %v, want [hello] (idle drains immediately)", sent)
	}
}

// A mid-turn send WITHOUT the queue flag (a plain Enter in the composer)
// goes straight to the platform so the running turn picks it up. It used
// to be force-queued, which delayed it until the whole turn ended.
func TestSessionMessage_BusySendsNowWithoutQueueFlag(t *testing.T) {
	srv, q := newQueueFake(t, true)

	if rr := postMessage(t, srv, "s1", `{"message":"interleave me"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}

	if sent := q.sentMessages(); len(sent) != 1 || sent[0] != "interleave me" {
		t.Fatalf("sent = %v, want [interleave me] (mid-turn send is not held)", sent)
	}
	if n, err := srv.stateDB.CountQueuedMessages(t.Context(), "fake", "s1"); err != nil || n != 0 {
		t.Fatalf("queued = %d (err %v), want 0", n, err)
	}
}

func TestSessionMessage_BusyQueuesThenFlushesOnIdle(t *testing.T) {
	srv, q := newQueueFake(t, true)

	// Two explicitly queued follow-ups (Ctrl+Enter): both accepted (204)
	// but neither sent.
	if rr := postMessage(t, srv, "s1", `{"message":"one","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("first post status = %d; body=%s", rr.Code, rr.Body)
	}
	if rr := postMessage(t, srv, "s1", `{"message":"two","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("second post status = %d; body=%s", rr.Code, rr.Body)
	}
	if sent := q.sentMessages(); len(sent) != 0 {
		t.Fatalf("sent = %v, want nothing while busy", sent)
	}
	// Turn finishes.
	q.setBusy(false)

	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent := q.sentMessages(); len(sent) != 1 || sent[0] != "one" {
		t.Fatalf("after 1st idle sent = %v, want [one] (one per turn, FIFO)", sent)
	}

	// Next idle edge sends the second.
	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent := q.sentMessages(); len(sent) != 2 || sent[1] != "two" {
		t.Fatalf("after 2nd idle sent = %v, want [one two]", sent)
	}
}

func TestQueueFlush_BlockedSessionDoesNotBlockIndependentSession(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	blocked := make(chan struct{})
	started := make(chan struct{})
	independent := make(chan struct{})
	reg.Register(&fakePlatform{
		id: "fake",
		sessions: []db.Session{
			mkSession("fake", "blocked", "blocked", 1),
			mkSession("fake", "independent", "independent", 1),
		},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			if req.SessionID == "blocked" {
				close(started)
				<-blocked
			} else {
				close(independent)
			}
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusDone}}, nil
		},
	})
	for _, id := range []string{"blocked", "independent"} {
		if err := srv.queueSvc().Enqueue(t.Context(), "fake", true, platforms.SendMessageRequest{SessionID: id, Message: id}); err != nil {
			t.Fatal(err)
		}
	}

	srv.onSessionIdle("fake", "blocked")
	<-started
	srv.onSessionIdle("fake", "independent")
	select {
	case <-independent:
	case <-t.Context().Done():
		t.Fatal("independent queue flush was blocked by another session")
	}
	close(blocked)
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionStatusReaderLatestMessageState(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	status := db.StatusWaiting
	messages := []db.Message{{ID: "assistant-1", TimeCreated: 200, Data: json.RawMessage(`{"role":"assistant","finish":"stop"}`)}}
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{
				Session:  &db.Session{ID: "s1", Status: status},
				Messages: messages,
			}, nil
		},
	})
	inferer := srv.queueModule()
	if running, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); running || !ok {
		t.Fatalf("waiting turn = (%v, %v), want (false, true)", running, ok)
	}

	if id, createdAt, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); id != "assistant-1" || createdAt != 200 || running || !ok || !completed {
		t.Fatalf("latest state = (%q, %d, %v, %v, %v), want (assistant-1, 200, false, true, true)", id, createdAt, running, completed, ok)
	}
	status = db.StatusBusy
	if running, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); !running || !ok {
		t.Fatalf("busy turn = (%v, %v), want (true, true)", running, ok)
	}
	if _, _, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); !ok || !running || completed {
		t.Fatalf("busy completion = (%v, %v), want (false, true)", completed, ok)
	}
	status = db.StatusWaiting
	messages[0].Data = json.RawMessage(`{"role":"user"}`)
	if _, _, _, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); !ok || completed {
		t.Fatalf("user completion = (%v, %v), want (false, true)", completed, ok)
	}
	messages[0].Data = json.RawMessage(`{`)
	if _, _, _, _, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); ok {
		t.Fatal("malformed message unexpectedly resolved")
	}
	messages = nil
	if id, createdAt, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); id != "" || createdAt != 0 || running || !completed || !ok {
		t.Fatalf("empty session = (%q, %d, %v, %v, %v), want resolved idle baseline", id, createdAt, running, completed, ok)
	}
	// No platform named falls back to reverse lookup, which cannot resolve
	// an unknown session.
	if _, _, _, _, ok := inferer.LatestMessageState(t.Context(), "", "missing"); ok {
		t.Fatal("missing session unexpectedly resolved")
	}
	// A named platform that isn't registered fails closed rather than
	// resolving the id on some other machine.
	if _, _, _, _, ok := inferer.LatestMessageState(t.Context(), "r-GONE:fake", "s1"); ok {
		t.Fatal("unregistered platform unexpectedly resolved")
	}
	if running, ok := inferer.TurnRunning(t.Context(), "r-GONE:fake", "s1"); running || ok {
		t.Fatalf("missing turn = (%v, %v), want (false, false)", running, ok)
	}
}

func TestSessionQueue_ListDeleteMove(t *testing.T) {
	srv, _ := newQueueFake(t, true)
	// Queue two follow-ups (Ctrl+Enter → queue:true holds them).
	postMessage(t, srv, "s1", `{"message":"one","queue":true}`)
	postMessage(t, srv, "s1", `{"message":"two","queue":true}`)

	// GET the queue.
	list := func() []queuedMessageView {
		req := httptest.NewRequest(http.MethodGet, "/api/session/s1/queue?platform=fake", nil)
		rr := httptest.NewRecorder()
		srv.handleSessionQueueList(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("list status = %d; body=%s", rr.Code, rr.Body)
		}
		var out []queuedMessageView
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}

	got := list()
	if len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("list = %+v, want [one two]", got)
	}

	// Move 'two' up → [two one].
	moveReq := httptest.NewRequest(http.MethodPost,
		"/api/session/s1/queue/"+got[1].ID+"/move?platform=fake",
		strings.NewReader(`{"direction":-1}`))
	moveRR := httptest.NewRecorder()
	srv.handleSessionQueueMove(moveRR, moveReq)
	if moveRR.Code != http.StatusNoContent {
		t.Fatalf("move status = %d; body=%s", moveRR.Code, moveRR.Body)
	}
	got = list()
	if got[0].Text != "two" || got[1].Text != "one" {
		t.Fatalf("after move = %+v, want [two one]", got)
	}

	// Delete the head.
	delReq := httptest.NewRequest(http.MethodDelete,
		"/api/session/s1/queue/"+got[0].ID+"?platform=fake", nil)
	delRR := httptest.NewRecorder()
	srv.handleSessionQueueDelete(delRR, delReq)
	if delRR.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d; body=%s", delRR.Code, delRR.Body)
	}
	got = list()
	if len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("after delete = %+v, want [one]", got)
	}
}

// A store failure (e.g. a schema drift where queued_message is missing a
// column) must surface as a 500 rather than an empty queue, and must be
// logged so it is diagnosable from the server side.
func TestSessionQueue_StoreFailureIs500(t *testing.T) {
	srv, _ := newSessionsTestServer(t)
	// Closing the state DB makes every queue store call fail.
	srv.stateDB.Close()

	for _, tc := range []struct {
		name string
		req  *http.Request
		fn   func(http.ResponseWriter, *http.Request)
	}{
		{
			"list",
			httptest.NewRequest(http.MethodGet, "/api/session/s1/queue?platform=fake", nil),
			srv.handleSessionQueueList,
		},
		{
			"delete",
			httptest.NewRequest(http.MethodDelete, "/api/session/s1/queue/q_1?platform=fake", nil),
			srv.handleSessionQueueDelete,
		},
		{
			"move",
			httptest.NewRequest(http.MethodPost, "/api/session/s1/queue/q_1/move?platform=fake",
				strings.NewReader(`{"direction":-1}`)),
			srv.handleSessionQueueMove,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.fn(rr, tc.req)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body)
			}
		})
	}
}

// drainQueueUpdated reads the subscriber channel until it sees a
// queue.updated, returning its decoded messages. Fails if none arrives or
// if it is not for session s1, which every caller uses.
func drainQueueUpdated(t *testing.T, ch <-chan broadcastEvent) []queuedMessageView {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.event != "ocman.queue.updated" {
				continue
			}
			var payload struct {
				SessionID string              `json:"sessionID"`
				Messages  []queuedMessageView `json:"messages"`
			}
			if err := json.Unmarshal(ev.data, &payload); err != nil {
				t.Fatalf("decode queue.updated: %v", err)
			}
			if payload.SessionID != "s1" {
				t.Fatalf("queue.updated sessionID = %q, want s1", payload.SessionID)
			}
			return payload.Messages
		case <-deadline:
			t.Fatal("no queue.updated broadcast within timeout")
			return nil
		}
	}
}

// End-to-end wire coverage: every queue mutation driven through the HTTP
// handlers must reach a subscribed /api/events client as a queue.updated
// carrying the correct full list — enqueue, drain (on idle Flush), remove,
// and move. This is what keeps the UI's list current without a refetch.
func TestQueueMutations_ReachTheWireAsQueueUpdated(t *testing.T) {
	srv, q := newQueueFake(t, true)

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	// --- Enqueue: two explicit holds (Ctrl+Enter). ---
	postMessage(t, srv, "s1", `{"message":"one","queue":true}`)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("after enqueue #1 = %+v, want [one]", got)
	}
	postMessage(t, srv, "s1", `{"message":"two","queue":true}`)
	// The broadcast carries the session's full queue, so clients apply it
	// directly without a refetch.
	if got := drainQueueUpdated(t, sub.ch); len(got) != 2 || got[0].Text != "one" || got[1].Text != "two" {
		t.Fatalf("after enqueue #2 = %+v, want [one two]", got)
	}

	// --- Move: bring 'two' to the front → [two one]. ---
	list, _ := srv.queueSvc().List(t.Context(), "fake", "s1")
	twoID := list[1].ID
	moveReq := httptest.NewRequest(http.MethodPost,
		"/api/session/s1/queue/"+twoID+"/move?platform=fake",
		strings.NewReader(`{"direction":-1}`))
	srv.handleSessionQueueMove(httptest.NewRecorder(), moveReq)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 2 || got[0].Text != "two" {
		t.Fatalf("after move = %+v, want [two one]", got)
	}

	// --- Remove: delete the head ('two') → [one]. ---
	delReq := httptest.NewRequest(http.MethodDelete,
		"/api/session/s1/queue/"+twoID+"?platform=fake", nil)
	srv.handleSessionQueueDelete(httptest.NewRecorder(), delReq)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("after remove = %+v, want [one]", got)
	}

	// --- Drain: session goes idle, Flush sends+deletes 'one' → []. ---
	q.setBusy(false)
	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := drainQueueUpdated(t, sub.ch); len(got) != 0 {
		t.Fatalf("after drain = %+v, want [] (empty list clears the UI)", got)
	}
}

// End-to-end through the real /api/events SSE handler: a drain must reach
// the browser as literal wire bytes `event: ocman.queue.updated` with
// `"messages":[]`. This is the layer between the broadcast and the browser
// that the in-process broadcast tests don't exercise — the empty-list
// frame is exactly what clears the UI.
func TestSSEStream_DrainDeliversEmptyQueueOverTheWire(t *testing.T) {
	srv, q := newQueueFake(t, true)

	// Queue one message mid-turn (row persisted, nothing sent).
	postMessage(t, srv, "s1", `{"message":"only","queue":true}`)

	// Open the real SSE stream.
	rr := &signalingRecorder{ResponseRecorder: httptest.NewRecorder(), ready: make(chan struct{}), event: make(chan struct{})}
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() { srv.handleGlobalEvents(rr, req); close(done) }()

	select {
	case <-rr.ready:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("handler never subscribed")
	}

	// Drain on idle: send + delete + broadcast empty list.
	q.setBusy(false)
	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-rr.event:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("queue.updated was not written")
	}
	cancel()
	<-done

	body := rr.Body.String()
	if !strings.Contains(body, "event: ocman.queue.updated") {
		t.Fatalf("no queue.updated event on the wire:\n%s", body)
	}
	// The drain frame must carry an EMPTY list — this is what clears the UI.
	if !strings.Contains(body, `"messages":[]`) {
		t.Fatalf("drain frame did not carry an empty messages list:\n%s", body)
	}
	// Must NOT be null (client ignores a null messages payload).
	if strings.Contains(body, `"messages":null`) {
		t.Fatalf("drain frame sent messages:null (client would ignore it):\n%s", body)
	}
}

type signalingRecorder struct {
	*httptest.ResponseRecorder
	ready     chan struct{}
	event     chan struct{}
	readyOnce sync.Once
	eventOnce sync.Once
}

func (r *signalingRecorder) Flush() {
	r.ResponseRecorder.Flush()
	r.readyOnce.Do(func() { close(r.ready) })
}

func (r *signalingRecorder) Write(p []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(p)
	if strings.Contains(string(p), "event: ocman.queue.updated") {
		r.eventOnce.Do(func() { close(r.event) })
	}
	return n, err
}

func TestSessionQueueMove_RejectsBadDirection(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{mkSession("fake", "s1", "t", 1)}})
	req := httptest.NewRequest(http.MethodPost,
		"/api/session/s1/queue/q_x/move?platform=fake",
		strings.NewReader(`{"direction":5}`))
	rr := httptest.NewRecorder()
	srv.handleSessionQueueMove(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for bad direction", rr.Code)
	}
}

func TestSessionQueueDelete_BestEffortOnMissing(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{mkSession("fake", "s1", "t", 1)}})
	// Deleting an id that isn't queued is a satisfied no-op → 204, not 404.
	req := httptest.NewRequest(http.MethodDelete,
		"/api/session/s1/queue/q_gone?platform=fake", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionQueueDelete(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (best-effort delete); body=%s", rr.Code, rr.Body)
	}
}

// A send into a session whose opencode instance is stale or gone must
// relaunch the project's instance via EnsureProjectOpencode and retry the
// send once. Worktree sessions run on the project's shared instance, so the
// relaunch folds the worktree back to the main checkout. When the relaunch
// fails, a direct send surfaces the error to the client (which owns the
// retry via the failed-send banner) instead of parking it in a queue the
// user never asked for, while a queued message stays queued so the next
// idle edge or sweep retries the relaunch too.
func TestSessionMessage_UnreachableRelaunchesOpencode(t *testing.T) {
	for _, tc := range []struct {
		name, dir, body string
		ensureErr       error
		flush           bool
		wantStatus      int
		wantAttempts    int
		wantQueued      []string
	}{
		{name: "relaunches and retries", dir: "/home/u/proj", body: `{"message":"hello"}`, wantStatus: http.StatusNoContent, wantAttempts: 2},
		{name: "folds worktree to project root", dir: "/home/u/.worktrees/proj/feat", body: `{"message":"hello"}`, wantStatus: http.StatusNoContent, wantAttempts: 2},
		{name: "failed relaunch surfaces direct send error", dir: "/home/u/proj", body: `{"message":"hello"}`, ensureErr: errors.New("tmux not available"), wantStatus: http.StatusServiceUnavailable, wantAttempts: 1},
		{name: "failed relaunch keeps queued message", dir: "/home/u/proj", body: `{"message":"hello","queue":true}`, ensureErr: errors.New("tmux not available"), flush: true, wantStatus: http.StatusNoContent, wantAttempts: 1, wantQueued: []string{"hello"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			var mu sync.Mutex
			var sent, ensuredDirs []string
			attempts := 0
			reg.Register(&fakePlatform{
				id:       "fake",
				sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
				sendMessageFn: func(req platforms.SendMessageRequest) error {
					mu.Lock()
					defer mu.Unlock()
					attempts++
					if attempts == 1 || tc.ensureErr != nil {
						return platforms.ErrPlatformUnreachable
					}
					sent = append(sent, req.Message)
					return nil
				},
				sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
					return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done", Directory: tc.dir}}, nil
				},
			})
			srv.hostRouter = hostsvc.NewRouter(&ensureHost{
				ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureRuntimeResult, error) {
					mu.Lock()
					ensuredDirs = append(ensuredDirs, req.ProjectDir)
					mu.Unlock()
					if tc.ensureErr != nil {
						return nil, tc.ensureErr
					}
					return &hostsvc.EnsureRuntimeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir, Launched: true}, nil
				},
			})

			if rr := postMessage(t, srv, "s1", tc.body); rr.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rr.Code, tc.wantStatus, rr.Body)
			}
			if tc.flush {
				srv.queueSvc().Flush(t.Context(), "fake", "s1")
			}
			mu.Lock()
			defer mu.Unlock()
			if len(ensuredDirs) != 1 || ensuredDirs[0] != "/home/u/proj" {
				t.Fatalf("ensured dirs = %v, want [/home/u/proj]", ensuredDirs)
			}
			if attempts != tc.wantAttempts {
				t.Fatalf("attempts = %d, want %d", attempts, tc.wantAttempts)
			}
			if tc.wantAttempts == 2 && (len(sent) != 1 || sent[0] != "hello") {
				t.Fatalf("sent = %v; want retry to deliver [hello]", sent)
			}
			list, _ := srv.queueSvc().List(t.Context(), "fake", "s1")
			var queued []string
			for _, m := range list {
				queued = append(queued, m.Text)
			}
			if !slices.Equal(queued, tc.wantQueued) {
				t.Fatalf("queue = %v, want %v", queued, tc.wantQueued)
			}
		})
	}
}

func TestSessionMessage_EmptyRejected(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
	})
	rr := postMessage(t, srv, "s1", `{"message":""}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for empty message; body=%s", rr.Code, rr.Body)
	}
}

// lifecycleFake is a fakePlatform that serves the bounded lifecycle read.
type lifecycleFake struct {
	*fakePlatform
	lifecycle *platforms.SessionLifecycle
	err       error
	calls     int
}

func TestQueueBusyEmptyLifecycle(t *testing.T) {
	for _, path := range []string{"enqueue", "sweep", "flush"} {
		t.Run(path, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			sends := 0
			reg.Register(&lifecycleFake{
				fakePlatform: &fakePlatform{
					id: "fake",
					sendMessageFn: func(platforms.SendMessageRequest) error {
						sends++
						return nil
					},
				},
				lifecycle: &platforms.SessionLifecycle{Status: db.StatusBusy},
			})
			queue := srv.queueSvc()
			if err := queue.Enqueue(t.Context(), "fake", path != "enqueue", platforms.SendMessageRequest{SessionID: "s1", Message: "held"}); err != nil {
				t.Fatal(err)
			}
			want := 0
			switch path {
			case "sweep":
				queue.Sweep(t.Context())
			case "flush":
				queue.Flush(t.Context(), "fake", "s1")
				want = 1 // An authoritative idle edge still wins over busy.
			}
			if sends != want {
				t.Fatalf("%s sent %d messages, want %d", path, sends, want)
			}
		})
	}
}

func (f *lifecycleFake) SessionLifecycle(context.Context, string) (*platforms.SessionLifecycle, error) {
	f.calls++
	return f.lifecycle, f.err
}

func TestSessionStatusReaderUsesBoundedLifecycle(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	detailReads := 0
	fake := &lifecycleFake{
		fakePlatform: &fakePlatform{
			id:       "fake",
			sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
			sessionDetailFn: func(string) (*platforms.SessionDetail, error) {
				detailReads++
				return &platforms.SessionDetail{Session: &db.Session{ID: "s1", Status: db.StatusBusy}}, nil
			},
		},
		lifecycle: &platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageID: "m2", LatestMessageCreated: 200, LatestMessageRole: "assistant"},
	}
	reg.Register(fake)
	inferer := srv.queueModule()

	if id, createdAt, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); id != "m2" || createdAt != 200 || running || !completed || !ok {
		t.Fatalf("latest = (%q, %d, %v, %v, %v), want completed m2", id, createdAt, running, completed, ok)
	}
	fake.lifecycle = &platforms.SessionLifecycle{Status: db.StatusBusy, LatestMessageID: "m2", LatestMessageRole: "assistant"}
	if running, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); !running || !ok {
		t.Fatalf("turn = (%v, %v), want running", running, ok)
	}
	if _, _, running, completed, ok := inferer.LatestMessageState(t.Context(), "", "s1"); !running || completed || !ok {
		t.Fatalf("reverse-lookup busy = (%v, %v, %v), want running", running, completed, ok)
	}
	fake.lifecycle = &platforms.SessionLifecycle{Status: db.StatusDone}
	if id, _, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); id != "" || running || !completed || !ok {
		t.Fatalf("empty = (%q, %v, %v, %v), want resolved idle baseline", id, running, completed, ok)
	}
	if fake.calls != 4 || detailReads != 0 {
		t.Fatalf("lifecycle calls = %d, detail reads = %d; want one bounded read per call and no detail reads", fake.calls, detailReads)
	}

	// A failed bounded read fails closed instead of paying for a detail read.
	fake.lifecycle, fake.err = nil, platforms.ErrNotFound
	if _, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); ok || detailReads != 0 {
		t.Fatalf("not-found resolved=%v detail reads=%d, want unresolved without detail read", ok, detailReads)
	}
	// An owner that cannot serve it (an older remote) falls back to Session.
	fake.err = errors.Join(platforms.ErrUnsupported, errors.New("unimplemented"))
	if running, ok := inferer.TurnRunning(t.Context(), "fake", "s1"); !running || !ok || detailReads != 1 {
		t.Fatalf("fallback = (%v, %v) after %d detail reads, want busy from Session", running, ok, detailReads)
	}
}
