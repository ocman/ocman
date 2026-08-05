package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestBroadcastSessionStatusCarriesPatch(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionStatus("s1", db.StatusBusy)
	ev := <-sub.ch
	var payload struct {
		SessionID string `json:"sessionID"`
		Patch     struct {
			Status db.SessionStatus `json:"status"`
		} `json:"patch"`
	}
	if err := json.Unmarshal(ev.data, &payload); err != nil {
		t.Fatal(err)
	}
	if ev.event != "ocman.session.changed" || payload.SessionID != "s1" || payload.Patch.Status != db.StatusBusy {
		t.Fatalf("unexpected event: %+v payload=%+v", ev, payload)
	}
}

func TestBroadcastHubFanOut(t *testing.T) {
	h := newBroadcastHub()

	sub1, unsub1 := h.subscribe()
	sub2, unsub2 := h.subscribe()
	defer unsub1()
	defer unsub2()

	if got := h.subscriberCount(); got != 2 {
		t.Fatalf("subscriberCount = %d, want 2", got)
	}

	h.broadcast("ocman.permission.resolved", []byte(`{"sessionID":"s1"}`))

	for i, ch := range []<-chan broadcastEvent{sub1.ch, sub2.ch} {
		select {
		case ev := <-ch:
			if ev.event != "ocman.permission.resolved" {
				t.Errorf("sub %d: event = %q, want ocman.permission.resolved", i, ev.event)
			}
			if string(ev.data) != `{"sessionID":"s1"}` {
				t.Errorf("sub %d: data = %q", i, string(ev.data))
			}
		case <-time.After(time.Second):
			t.Fatalf("sub %d: no event received", i)
		}
	}
}

func TestBroadcastHubUnsubscribeStopsDelivery(t *testing.T) {
	h := newBroadcastHub()
	sub, unsub := h.subscribe()
	unsub()

	if got := h.subscriberCount(); got != 0 {
		t.Fatalf("subscriberCount after unsub = %d, want 0", got)
	}

	// Channel is closed; broadcast must not panic and the channel must
	// be drained/closed.
	h.broadcast("x", []byte("y"))
	if _, open := <-sub.ch; open {
		t.Fatal("expected closed channel after unsubscribe")
	}

	// Double unsubscribe is a no-op.
	unsub()
}

func TestBroadcastHubNonBlockingOnFullBuffer(t *testing.T) {
	h := newBroadcastHub()
	_, unsub := h.subscribe()
	defer unsub()

	// Overflow the 16-slot buffer; broadcast must not block.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			h.broadcast("e", []byte("d"))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("broadcast blocked on a full subscriber buffer")
	}
}

// A coalescing event (queue.updated) must NOT be dropped when the buffer
// is full — the latest full-state snapshot is parked as pending and the
// subscriber is woken to flush it.
func TestBroadcastHubCoalescesQueueUpdatedOnFullBuffer(t *testing.T) {
	h := newBroadcastHub()
	sub, unsub := h.subscribe()
	defer unsub()

	// Fill the 16-slot buffer with non-coalescing events so it's full.
	for i := 0; i < 16; i++ {
		h.broadcast("ocman.session.idle", []byte(`{"sessionID":"s1"}`))
	}

	// Now several queue.updated for the same session can't fit — they must
	// coalesce to the latest, not drop.
	h.broadcast("ocman.queue.updated", []byte(`{"sessionID":"s1","messages":[{"id":"a"}]}`))
	h.broadcast("ocman.queue.updated", []byte(`{"sessionID":"s1","messages":[]}`))

	select {
	case <-sub.wake:
	case <-time.After(time.Second):
		t.Fatal("subscriber was never woken for a coalesced event")
	}
	pending := sub.drainPending()
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1 (coalesced latest-wins)", len(pending))
	}
	if got := string(pending[0].data); got != `{"sessionID":"s1","messages":[]}` {
		t.Fatalf("pending payload = %q, want the latest (empty) snapshot", got)
	}
}

func TestHandleGlobalEventsStreamsBroadcast(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	// Run the handler in a goroutine; it blocks until ctx is cancelled.
	done := make(chan struct{})
	go func() {
		srv.handleGlobalEvents(rr, req)
		close(done)
	}()

	// Wait for the subscriber to register before broadcasting.
	deadline := time.Now().Add(time.Second)
	for srv.broadcastHub.subscriberCount() == 0 {
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("handler never subscribed")
		}
		time.Sleep(time.Millisecond)
	}

	srv.broadcastGlobalEvent("ocman.permission.resolved", []byte(`{"sessionID":"abc","permissionId":"p1"}`))

	// Give the handler a moment to write, then cancel to end the stream.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	body := rr.Body.String()
	if !strings.Contains(body, "event: ocman.permission.resolved") {
		t.Errorf("missing event line in body: %q", body)
	}
	if !strings.Contains(body, `"sessionID":"abc"`) {
		t.Errorf("missing payload in body: %q", body)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
