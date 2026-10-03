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

func TestBroadcastSessionTitleCarriesPatch(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionTitle("s1", "Renamed")
	ev := <-sub.ch
	if ev.event != "ocman.session.changed" || string(ev.data) != `{"patch":{"title":"Renamed"},"sessionID":"s1"}` {
		t.Fatalf("unexpected event: %s %s", ev.event, ev.data)
	}
}

// A full buffer must not let a later status patch erase a pending title.
func TestBroadcastHubMergesParkedSessionPatches(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()
	for i := 0; i < cap(sub.ch); i++ {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}

	srv.broadcastSessionTitle("s1", "Renamed")
	srv.broadcastSessionStatus("s1", db.StatusBusy)
	pending := sub.drainPending()
	if len(pending) != 1 || string(pending[0].data) != `{"patch":{"status":"busy","title":"Renamed"},"sessionID":"s1"}` {
		t.Fatalf("pending = %+v", pending)
	}

	// An identity-only event asks for a refetch, and that request survives
	// a later patch.
	srv.broadcastSessionTitle("s1", "Again")
	srv.broadcastSessionChanged("s1")
	srv.broadcastSessionStatus("s1", db.StatusDone)
	pending = sub.drainPending()
	if len(pending) != 1 || string(pending[0].data) != `{"sessionID":"s1"}` {
		t.Fatalf("pending = %+v", pending)
	}
}

// A slow client must see a session's changes in publish order: an older
// buffered title must not be written after a newer parked one, and once a
// key is parked a later event for it must not overtake via the buffer.
func TestBroadcastHubKeepsSessionOrderAcrossBufferAndPark(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionTitle("s1", "Old") // buffered
	for len(sub.ch) < cap(sub.ch) {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}
	srv.broadcastSessionTitle("s1", "New")    // parked
	<-sub.ch                                  // the writer frees one slot...
	srv.broadcastSessionTitle("s1", "Newest") // ...but s1 stays parked

	var titles []string
	collect := func(ev broadcastEvent) {
		var p struct {
			Patch struct {
				Title string `json:"title"`
			} `json:"patch"`
		}
		if ev.event == "ocman.session.changed" && json.Unmarshal(ev.data, &p) == nil {
			titles = append(titles, p.Patch.Title)
		}
	}
	for _, ev := range sub.takeBatch() {
		collect(ev)
	}
	// "Old" was the slot freed above; what remains must end on the newest.
	if len(titles) != 1 || titles[0] != "Newest" {
		t.Fatalf("titles written = %v, want [Newest]", titles)
	}

	// Old buffered + new parked: the buffered one is written first.
	srv.broadcastSessionTitle("s1", "A")
	for len(sub.ch) < cap(sub.ch) {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}
	srv.broadcastSessionTitle("s1", "B")
	titles = nil
	for _, ev := range sub.takeBatch() {
		collect(ev)
	}
	if len(titles) != 2 || titles[0] != "A" || titles[1] != "B" {
		t.Fatalf("titles written = %v, want [A B]", titles)
	}

	// Events published while the writer is still writing a batch: a session
	// first seen mid-flush gains a buffered A and a parked B. They must still
	// be written A before B.
	for len(sub.ch) < cap(sub.ch) {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}
	srv.broadcastSessionTitle("other", "parked")
	titles = nil
	for i, ev := range sub.takeBatch() {
		if i == 0 {
			for len(sub.ch) < cap(sub.ch)-1 {
				srv.broadcastGlobalEvent("filler", []byte(`{}`))
			}
			srv.broadcastSessionTitle("x", "A") // last free slot
			srv.broadcastSessionTitle("x", "B") // parked
		}
		_ = ev
	}
	for _, ev := range sub.takeBatch() {
		if coalesceKey(ev.event, ev.data) == "ocman.session.changed\x00x" {
			collect(ev)
		}
	}
	if len(titles) != 2 || titles[0] != "A" || titles[1] != "B" {
		t.Fatalf("mid-flush titles written = %v, want [A B]", titles)
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

// #490: ocman.session.idle and ocman.session.changed drive queue drain,
// notification state, and the sidebar. They are identity-keyed (the
// consumer re-reads state), so on a full buffer they must coalesce
// last-write-wins per session instead of being silently dropped.
func TestBroadcastHubCoalescesSessionEventsOnFullBuffer(t *testing.T) {
	h := newBroadcastHub()
	sub, unsub := h.subscribe()
	defer unsub()

	// Fill the 16-slot buffer with unrelated events so it's full.
	for i := 0; i < 16; i++ {
		h.broadcast("filler", []byte(`{}`))
	}

	// A burst of idle/changed events for two sessions can't fit — they
	// must park last-write-wins per (event, session), not drop.
	h.broadcast("ocman.session.idle", []byte(`{"sessionID":"s1"}`))
	h.broadcast("ocman.session.changed", []byte(`{"sessionID":"s1"}`))
	h.broadcast("ocman.session.changed", []byte(`{"sessionID":"s1","patch":{"status":"busy"}}`))
	h.broadcast("ocman.session.changed", []byte(`{"sessionID":"s2"}`))

	select {
	case <-sub.wake:
	case <-time.After(time.Second):
		t.Fatal("subscriber was never woken for coalesced session events")
	}
	pending := sub.drainPending()
	if len(pending) != 3 {
		t.Fatalf("pending = %d, want 3 (idle s1, changed s1, changed s2)", len(pending))
	}
	byKey := make(map[string]string, len(pending))
	for _, ev := range pending {
		byKey[coalesceKey(ev.event, ev.data)] = string(ev.data)
	}
	// The pending refetch request outlives the later patch: a refetch
	// already re-reads the status the patch carried.
	if got := byKey["ocman.session.changed\x00s1"]; got != `{"sessionID":"s1"}` {
		t.Fatalf("changed s1 payload = %q, want the identity-only refetch request", got)
	}
	if _, ok := byKey["ocman.session.idle\x00s1"]; !ok {
		t.Fatal("idle s1 was dropped instead of parked")
	}
	if _, ok := byKey["ocman.session.changed\x00s2"]; !ok {
		t.Fatal("changed s2 was dropped instead of parked")
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
