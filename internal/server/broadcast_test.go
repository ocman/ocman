package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/db"
)

func TestBroadcastSessionStatusCarriesPatch(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionStatus("opencode", "s1", db.StatusBusy)
	ev := <-sub.ch
	var payload struct {
		SessionID string `json:"sessionID"`
		Platform  string `json:"platform"`
		Patch     struct {
			Status db.SessionStatus `json:"status"`
		} `json:"patch"`
	}
	if err := json.Unmarshal(ev.data, &payload); err != nil {
		t.Fatal(err)
	}
	if ev.event != "ocman.session.changed" || payload.SessionID != "s1" || payload.Platform != "opencode" || payload.Patch.Status != db.StatusBusy {
		t.Fatalf("unexpected event: %+v payload=%+v", ev, payload)
	}
}

func TestBroadcastSessionTitleCarriesPatch(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionTitle("claudecode", "s1", "Renamed")
	ev := <-sub.ch
	if ev.event != "ocman.session.changed" || string(ev.data) != `{"patch":{"title":"Renamed"},"platform":"claudecode","sessionID":"s1"}` {
		t.Fatalf("unexpected event: %s %s", ev.event, ev.data)
	}
}

// A session change is stamped with the caller's platform, not OpenCode's.
func TestBroadcastSessionChangedCarriesCallerPlatform(t *testing.T) {
	srv := &Server{broadcastHub: newBroadcastHub()}
	sub, unsubscribe := srv.broadcastHub.subscribe()
	defer unsubscribe()

	srv.broadcastSessionChanged("claudecode", "s1")
	ev := <-sub.ch
	if ev.event != "ocman.session.changed" || string(ev.data) != `{"platform":"claudecode","sessionID":"s1"}` {
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

	srv.broadcastSessionTitle("opencode", "s1", "Renamed")
	srv.broadcastSessionStatus("opencode", "s1", db.StatusBusy)
	pending := sub.drainPending()
	if len(pending) != 1 || string(pending[0].data) != `{"patch":{"status":"busy","title":"Renamed"},"platform":"opencode","sessionID":"s1"}` {
		t.Fatalf("pending = %+v", pending)
	}

	// An identity-only event asks for a refetch, and that request survives
	// a later patch.
	srv.broadcastSessionTitle("opencode", "s1", "Again")
	srv.broadcastSessionChanged("opencode", "s1")
	srv.broadcastSessionStatus("opencode", "s1", db.StatusDone)
	pending = sub.drainPending()
	if len(pending) != 1 || string(pending[0].data) != `{"platform":"opencode","sessionID":"s1"}` {
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

	srv.broadcastSessionTitle("opencode", "s1", "Old") // buffered
	for len(sub.ch) < cap(sub.ch) {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}
	srv.broadcastSessionTitle("opencode", "s1", "New")    // parked
	<-sub.ch                                  // the writer frees one slot...
	srv.broadcastSessionTitle("opencode", "s1", "Newest") // ...but s1 stays parked

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
	srv.broadcastSessionTitle("opencode", "s1", "A")
	for len(sub.ch) < cap(sub.ch) {
		srv.broadcastGlobalEvent("filler", []byte(`{}`))
	}
	srv.broadcastSessionTitle("opencode", "s1", "B")
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
	srv.broadcastSessionTitle("opencode", "other", "parked")
	titles = nil
	for i, ev := range sub.takeBatch() {
		if i == 0 {
			for len(sub.ch) < cap(sub.ch)-1 {
				srv.broadcastGlobalEvent("filler", []byte(`{}`))
			}
			srv.broadcastSessionTitle("opencode", "x", "A") // last free slot
			srv.broadcastSessionTitle("opencode", "x", "B") // parked
		}
		_ = ev
	}
	for _, ev := range sub.takeBatch() {
		if coalesceKey(ev.event, ev.data) == "ocman.session.changed\x00opencode\x00x" {
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

// deliver must use the key broadcast computed once per event, not re-parse
// the payload per subscriber.
func TestBroadcastSubDeliverUsesPrecomputedKey(t *testing.T) {
	h := newBroadcastHub()
	sub, unsub := h.subscribe()
	defer unsub()
	for len(sub.ch) < cap(sub.ch) {
		h.broadcast("filler", []byte(`{}`))
	}
	sub.deliver(broadcastEvent{event: "ocman.queue.updated", data: []byte(`{"sessionID":"s1"}`), key: "precomputed"})
	if _, ok := sub.pending["precomputed"]; !ok || len(sub.pending) != 1 {
		t.Fatalf("pending keys = %v, want [precomputed]", sub.pending)
	}
}

// Merged changes are shared by every subscriber's park: merging for one
// must not leak into another's pending payload.
func TestBroadcastHubMergeDoesNotShareStateAcrossSubscribers(t *testing.T) {
	h := newBroadcastHub()
	a, unsubA := h.subscribe()
	defer unsubA()
	b, unsubB := h.subscribe()
	defer unsubB()
	for len(a.ch) < cap(a.ch) {
		h.broadcast("filler", []byte(`{}`))
	}
	<-b.ch // b keeps one free slot, so its first change is buffered
	h.broadcast("ocman.session.changed", []byte(`{"sessionID":"s1","patch":{"title":"T"}}`))
	h.broadcast("ocman.session.changed", []byte(`{"sessionID":"s1","patch":{"status":"busy"}}`))
	if got := a.drainPending(); len(got) != 1 || string(got[0].data) != `{"patch":{"status":"busy","title":"T"},"sessionID":"s1"}` {
		t.Fatalf("a pending = %+v", got)
	}
	if got := b.drainPending(); len(got) != 1 || string(got[0].data) != `{"sessionID":"s1","patch":{"status":"busy"}}` {
		t.Fatalf("b pending = %+v", got)
	}
}

// A client that stops reading is disconnected by the write deadline and
// unsubscribed, instead of pinning the writer until TCP gives up.
func TestHandleGlobalEventsDropsStalledClient(t *testing.T) {
	old := sseWriteTimeout
	sseWriteTimeout = 50 * time.Millisecond
	t.Cleanup(func() { sseWriteTimeout = old })

	srv := &Server{broadcastHub: newBroadcastHub()}
	// The production middleware chain, so its writer wrappers must pass
	// deadlines and flush errors through.
	ts := httptest.NewServer(withSecurityHeaders(withRequestTiming(withOTel(http.HandlerFunc(srv.handleGlobalEvents)))))
	defer ts.Close()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close() // never read: the client is stalled

	big := []byte(`"` + strings.Repeat("x", 1<<20) + `"`)
	deadline := time.Now().Add(10 * time.Second)
	for srv.broadcastHub.subscriberCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("stalled client was never unsubscribed")
		}
		srv.broadcastGlobalEvent("big", big)
		time.Sleep(5 * time.Millisecond)
	}
}

func BenchmarkBroadcastSessionChangedFanOut(b *testing.B) {
	h := newBroadcastHub()
	for range 50 {
		_, unsub := h.subscribe()
		b.Cleanup(unsub)
	}
	data := []byte(`{"sessionID":"s1","platform":"opencode","patch":{"status":"busy"}}`)
	for b.Loop() {
		h.broadcast("ocman.session.changed", data)
	}
}

// Concurrent publications reach every subscriber in the same order, both
// through the buffer and when parked: clients apply full snapshots as-is.
func TestBroadcastHubConcurrentPublicationsKeepOneOrder(t *testing.T) {
	for range 200 {
		h := newBroadcastHub()
		a, unsubA := h.subscribe()
		b, unsubB := h.subscribe()
		var wg sync.WaitGroup
		for i := range 24 { // more than the buffer, so the tail parks
			wg.Add(1)
			go func() {
				defer wg.Done()
				h.broadcast("ocman.queue.updated", fmt.Appendf(nil, `{"sessionID":"s1","n":%d}`, i))
			}()
		}
		wg.Wait()
		order := func(sub *broadcastSub) (out []string) {
			for _, ev := range sub.takeBatch() {
				out = append(out, string(ev.data))
			}
			return out
		}
		if ga, gb := order(a), order(b); !slices.Equal(ga, gb) {
			t.Fatalf("subscribers saw different orders:\n%v\n%v", ga, gb)
		}
		unsubA()
		unsubB()
	}
}
