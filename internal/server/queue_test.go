package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
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
			status := "done"
			if busy {
				status = "busy"
			}
			mu.Unlock()
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

	// Two follow-ups while busy: both accepted (204) but neither sent.
	if rr := postMessage(t, srv, "s1", `{"message":"one"}`); rr.Code != http.StatusNoContent {
		t.Fatalf("first post status = %d; body=%s", rr.Code, rr.Body)
	}
	if rr := postMessage(t, srv, "s1", `{"message":"two"}`); rr.Code != http.StatusNoContent {
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

	// onSessionIdle flushes in a goroutine; call Flush synchronously to
	// avoid a race in the test (same code path, no goroutine).
	srv.queueSvc().Flush(t.Context(), "", "s1")
	mu.Lock()
	if len(sent) != 1 || sent[0] != "one" {
		mu.Unlock()
		t.Fatalf("after 1st idle sent = %v, want [one] (one per turn, FIFO)", sent)
	}
	mu.Unlock()

	// Next idle edge sends the second.
	srv.queueSvc().Flush(t.Context(), "", "s1")
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 2 || sent[1] != "two" {
		t.Fatalf("after 2nd idle sent = %v, want [one two]", sent)
	}
}

func TestSessionQueue_ListDeleteMove(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		// Busy so the two posts queue instead of draining.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})
	// Queue two follow-ups.
	postMessage(t, srv, "s1", `{"message":"one"}`)
	postMessage(t, srv, "s1", `{"message":"two"}`)

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

// The queue.updated broadcast must carry the session's full queue so
// clients apply it directly without a refetch.
func TestBroadcastQueueUpdated_CarriesFullQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{
		id:            "fake",
		sessions:      []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(platforms.SendMessageRequest) error { return nil },
		// Busy so the posts queue instead of draining.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: "busy"}}, nil
		},
	})

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	postMessage(t, srv, "s1", `{"message":"one"}`)
	postMessage(t, srv, "s1", `{"message":"two"}`)

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
			status := "done"
			if busy {
				status = "busy"
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	// --- Enqueue: two mid-turn holds. ---
	postMessage(t, srv, "s1", `{"message":"one"}`)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 1 || got[0].Text != "one" {
		t.Fatalf("after enqueue #1 = %+v, want [one]", got)
	}
	postMessage(t, srv, "s1", `{"message":"two"}`)
	if got := drainQueueUpdated(t, sub.ch); len(got) != 2 {
		t.Fatalf("after enqueue #2 = %+v, want 2 items", got)
	}

	// --- Move: bring 'two' to the front → [two one]. ---
	list, _ := srv.queueSvc().List("fake", "s1")
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
	srv.queueSvc().Flush(t.Context(), "", "s1")
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
			status := "done"
			if busy {
				status = "busy"
			}
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: status}}, nil
		},
	})

	// Queue one message mid-turn (row persisted, nothing sent).
	postMessage(t, srv, "s1", `{"message":"only"}`)

	// Open the real SSE stream.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() { srv.handleGlobalEvents(rr, req); close(done) }()

	deadline := time.Now().Add(time.Second)
	for srv.broadcastHub.subscriberCount() == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("handler never subscribed")
		}
		time.Sleep(time.Millisecond)
	}

	// Drain on idle: send + delete + broadcast empty list.
	busy = false
	srv.queueSvc().Flush(t.Context(), "", "s1")

	time.Sleep(30 * time.Millisecond)
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
