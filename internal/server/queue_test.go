package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/hostsvc"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func postMessage(t *testing.T, srv *Server, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost,
		"/api/session/"+sessionID+"/message?platform=fake",
		strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleSessionMessage(rr, req)
	return rr
}

func TestSessionMessage_IdleSendsImmediately(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	var sent []string
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			mu.Lock()
			sent = append(sent, req.Message)
			mu.Unlock()
			return nil
		},
		// Idle session.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done"}}, nil
		},
	})

	rr := postMessage(t, srv, "s1", `{"message":"hello"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != "hello" {
		t.Fatalf("sent = %v, want [hello] (idle drains immediately)", sent)
	}
}

// A mid-turn send WITHOUT the queue flag (a plain Enter in the composer)
// goes straight to the platform so the running turn picks it up. It used
// to be force-queued, which delayed it until the whole turn ended.
func TestSessionMessage_BusySendsNowWithoutQueueFlag(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	var sent []string
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			mu.Lock()
			sent = append(sent, req.Message)
			mu.Unlock()
			return nil
		},
		// Mid-turn for the whole test.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})

	if rr := postMessage(t, srv, "s1", `{"message":"interleave me"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0] != "interleave me" {
		t.Fatalf("sent = %v, want [interleave me] (mid-turn send is not held)", sent)
	}
	if n, err := srv.stateDB.CountQueuedMessages(t.Context(), "fake", "s1"); err != nil || n != 0 {
		t.Fatalf("queued = %d (err %v), want 0", n, err)
	}
}

func TestSessionMessage_BusyQueuesThenFlushesOnIdle(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	var sent []string
	busy := true
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			mu.Lock()
			sent = append(sent, req.Message)
			mu.Unlock()
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			mu.Lock()
			status := db.StatusDone
			if busy {
				status = db.StatusBusy
			}
			mu.Unlock()
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

	// Two explicitly queued follow-ups (Ctrl+Enter): both accepted (204)
	// but neither sent.
	if rr := postMessage(t, srv, "s1", `{"message":"one","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("first post status = %d; body=%s", rr.Code, rr.Body)
	}
	if rr := postMessage(t, srv, "s1", `{"message":"two","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("second post status = %d; body=%s", rr.Code, rr.Body)
	}
	mu.Lock()
	if len(sent) != 0 {
		mu.Unlock()
		t.Fatalf("sent = %v, want nothing while busy", sent)
	}
	// Turn finishes.
	busy = false
	mu.Unlock()

	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(sent) != 1 || sent[0] != "one" {
		mu.Unlock()
		t.Fatalf("after 1st idle sent = %v, want [one] (one per turn, FIFO)", sent)
	}
	mu.Unlock()

	// Next idle edge sends the second.
	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || sent[1] != "two" {
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

func TestWorkflowStatusInferer_LatestMessageState(t *testing.T) {
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
	inferer := &workflowStatusInferer{s: srv}

	if id, createdAt, running, completed, ok := inferer.LatestMessageState(t.Context(), "fake", "s1"); id != "assistant-1" || createdAt != 200 || running || !ok || !completed {
		t.Fatalf("latest state = (%q, %d, %v, %v, %v), want (assistant-1, 200, false, true, true)", id, createdAt, running, completed, ok)
	}
	status = db.StatusBusy
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
	// No platform named: the caller (a hand-written workflow trigger) falls
	// back to the reverse lookup, which cannot resolve an unknown session.
	if _, _, _, _, ok := inferer.LatestMessageState(t.Context(), "", "missing"); ok {
		t.Fatal("missing session unexpectedly resolved")
	}
	// A named platform that isn't registered fails closed rather than
	// resolving the id on some other machine.
	if _, _, _, _, ok := inferer.LatestMessageState(t.Context(), "r-GONE:fake", "s1"); ok {
		t.Fatal("unregistered platform unexpectedly resolved")
	}
}

func TestSessionQueue_ListDeleteMove(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:            "fake",
		sessions:      []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})
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

// The queue.updated broadcast must carry the session's full queue so
// clients apply it directly without a refetch.
func TestBroadcastQueueUpdated_CarriesFullQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:            "fake",
		sessions:      []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	postMessage(t, srv, "s1", `{"message":"one","queue":true}`)
	postMessage(t, srv, "s1", `{"message":"two","queue":true}`)

	// Drain the buffered broadcasts; the last one reflects the full queue.
	var last broadcastEvent
	for got := 0; got < 2; got++ {
		last = <-sub.ch
		if last.event != "ocman.queue.updated" {
			t.Fatalf("event = %q, want ocman.queue.updated", last.event)
		}
	}

	var payload struct {
		SessionID string              `json:"sessionID"`
		Messages  []queuedMessageView `json:"messages"`
	}
	if err := json.Unmarshal(last.data, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.SessionID != "s1" {
		t.Fatalf("sessionID = %q, want s1", payload.SessionID)
	}
	if len(payload.Messages) != 2 || payload.Messages[0].Text != "one" || payload.Messages[1].Text != "two" {
		t.Fatalf("messages = %+v, want [one two]", payload.Messages)
	}
}

// drainQueueUpdated reads the subscriber channel until it sees a
// queue.updated, returning its decoded messages. Fails if none arrives.
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
	srv, reg := newSessionsTestServer(t)
	busy := true
	reg.Register(&fakePlatform{
		id:            "fake",
		sessions:      []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			status := db.StatusDone
			if busy {
				status = db.StatusBusy
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	// --- Enqueue: two explicit holds (Ctrl+Enter). ---
	postMessage(t, srv, "s1", `{"message":"one","queue":true}`)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("after enqueue #1 = %+v, want [one]", got)
	}
	postMessage(t, srv, "s1", `{"message":"two","queue":true}`)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 2 {
		t.Fatalf("after enqueue #2 = %+v, want 2 items", got)
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
	busy = false
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
	srv, reg := newSessionsTestServer(t)
	busy := true
	reg.Register(&fakePlatform{
		id:            "fake",
		sessions:      []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			status := db.StatusDone
			if busy {
				status = db.StatusBusy
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

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
	busy = false
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

// ensureStubHost is a hostsvc.Host double whose only real method is
// EnsureProjectOpencode; everything else panics via the nil embedded
// interface (tests here never call it).
type ensureStubHost struct {
	hostsvc.Host
	ensure func(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error)
}

func (h *ensureStubHost) EnsureProjectOpencode(ctx context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
	return h.ensure(ctx, req)
}

// A send into a session whose opencode instance is stale/gone must
// relaunch the project's instance via EnsureProjectOpencode and retry
// the send once.
func TestSessionMessage_UnreachableRelaunchesOpencodeAndRetries(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	var sent []string
	attempts := 0
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts == 1 {
				return platforms.ErrPlatformUnreachable
			}
			sent = append(sent, req.Message)
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done", Directory: "/home/u/proj"}}, nil
		},
	})
	var ensuredDirs []string
	srv.hostRouter = hostsvc.NewRouter(&ensureStubHost{
		ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			mu.Lock()
			ensuredDirs = append(ensuredDirs, req.ProjectDir)
			mu.Unlock()
			return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir, Launched: true}, nil
		},
	})

	rr := postMessage(t, srv, "s1", `{"message":"hello"}`)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rr.Code, rr.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ensuredDirs) != 1 || ensuredDirs[0] != "/home/u/proj" {
		t.Fatalf("ensured dirs = %v, want [/home/u/proj]", ensuredDirs)
	}
	if attempts != 2 || len(sent) != 1 || sent[0] != "hello" {
		t.Fatalf("attempts = %d, sent = %v; want retry to deliver [hello]", attempts, sent)
	}
	// Delivered: the queue must be empty.
	if list, _ := srv.queueSvc().List(t.Context(), "fake", "s1"); len(list) != 0 {
		t.Fatalf("queue = %+v, want empty after delivered retry", list)
	}
}

// Worktree sessions run on the project's shared instance: the relaunch
// must fold the worktree directory back to the main checkout.
func TestSessionMessage_RelaunchFoldsWorktreeToProjectRoot(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	attempts := 0
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			if attempts == 1 {
				return platforms.ErrPlatformUnreachable
			}
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done", Directory: "/home/u/.worktrees/proj/feat"}}, nil
		},
	})
	var ensuredDirs []string
	srv.hostRouter = hostsvc.NewRouter(&ensureStubHost{
		ensure: func(_ context.Context, req hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			mu.Lock()
			ensuredDirs = append(ensuredDirs, req.ProjectDir)
			mu.Unlock()
			return &hostsvc.EnsureProjectOpencodeResult{Endpoint: "http://127.0.0.1:5599", RepoRoot: req.ProjectDir}, nil
		},
	})

	if rr := postMessage(t, srv, "s1", `{"message":"hello"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(ensuredDirs) != 1 || ensuredDirs[0] != "/home/u/proj" {
		t.Fatalf("ensured dirs = %v, want folded [/home/u/proj]", ensuredDirs)
	}
}

// A direct send whose relaunch also fails surfaces the error to the
// client (which owns the retry via the failed-send banner) instead of
// silently parking the message in a queue the user never asked for.
func TestSessionMessage_RelaunchFails_DirectSendSurfacesError(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	attempts := 0
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			return platforms.ErrPlatformUnreachable
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done", Directory: "/home/u/proj"}}, nil
		},
	})
	srv.hostRouter = hostsvc.NewRouter(&ensureStubHost{
		ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			return nil, errors.New("tmux not available")
		},
	})

	if rr := postMessage(t, srv, "s1", `{"message":"hello"}`); rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (no retry when relaunch failed)", attempts)
	}
	if list, _ := srv.queueSvc().List(t.Context(), "fake", "s1"); len(list) != 0 {
		t.Fatalf("queue = %+v, want empty (a direct send is not parked)", list)
	}
}

// When the relaunch itself fails while draining a queued message, the
// message must stay queued so the next idle edge / sweep retries (which
// retries the relaunch too).
func TestSessionMessage_RelaunchFails_MessageStaysQueued(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var mu sync.Mutex
	attempts := 0
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error {
			mu.Lock()
			defer mu.Unlock()
			attempts++
			return platforms.ErrPlatformUnreachable
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "done", Directory: "/home/u/proj"}}, nil
		},
	})
	srv.hostRouter = hostsvc.NewRouter(&ensureStubHost{
		ensure: func(context.Context, hostsvc.EnsureProjectOpencodeRequest) (*hostsvc.EnsureProjectOpencodeResult, error) {
			return nil, errors.New("tmux not available")
		},
	})

	// Enqueue accepts (204); the idle-edge drain then fails and the row stays.
	if rr := postMessage(t, srv, "s1", `{"message":"hello","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
	}
	srv.queueSvc().Flush(t.Context(), "fake", "s1")
	mu.Lock()
	if attempts != 1 {
		mu.Unlock()
		t.Fatalf("attempts = %d, want 1 (no retry when relaunch failed)", attempts)
	}
	mu.Unlock()
	if list, _ := srv.queueSvc().List(t.Context(), "fake", "s1"); len(list) != 1 || list[0].Text != "hello" {
		t.Fatalf("queue = %+v, want [hello] retained for a later retry", list)
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
