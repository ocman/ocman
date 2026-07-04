package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBroadcastHubFanOut(t *testing.T) {
	h := newBroadcastHub()

	ch1, unsub1 := h.subscribe()
	ch2, unsub2 := h.subscribe()
	defer unsub1()
	defer unsub2()

	if got := h.subscriberCount(); got != 2 {
		t.Fatalf("subscriberCount = %d, want 2", got)
	}

	h.broadcast("ocman.permission.resolved", []byte(`{"sessionID":"s1"}`))

	for i, ch := range []<-chan broadcastEvent{ch1, ch2} {
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
	ch, unsub := h.subscribe()
	unsub()

	if got := h.subscriberCount(); got != 0 {
		t.Fatalf("subscriberCount after unsub = %d, want 0", got)
	}

	// Channel is closed; broadcast must not panic and the channel must
	// be drained/closed.
	h.broadcast("x", []byte("y"))
	if _, open := <-ch; open {
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
