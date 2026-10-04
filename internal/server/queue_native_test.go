package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// nativeQueuePlatform is a fakePlatform that also holds follow-ups itself
// (platforms.NativeQueue), like OpenCode v2.
type nativeQueuePlatform struct {
	*fakePlatform

	mu        sync.Mutex
	listErr   error // returned by NativeQueued
	sendErr   error // returned by a Delivery "queue" send
	cancelErr error
	held      []platforms.NativeQueuedMessage
	sends     []platforms.SendMessageRequest
	cancelled []platforms.CancelNativeQueuedRequest
}

var _ platforms.NativeQueue = (*nativeQueuePlatform)(nil)

func newNativeQueuePlatform() *nativeQueuePlatform {
	p := &nativeQueuePlatform{}
	p.fakePlatform = &fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.sends = append(p.sends, req)
			if req.Delivery != "queue" {
				return nil
			}
			if p.sendErr != nil {
				return p.sendErr
			}
			p.held = append(p.held, platforms.NativeQueuedMessage{ID: "msg_native", Text: req.Message, CreatedAt: 42})
			return nil
		},
		// Mid-turn throughout, so ocman's queue holds instead of sending.
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusBusy}}, nil
		},
	}
	return p
}

func (p *nativeQueuePlatform) NativeQueued(_ context.Context, _ string) ([]platforms.NativeQueuedMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.listErr != nil {
		return nil, p.listErr
	}
	return append([]platforms.NativeQueuedMessage(nil), p.held...), nil
}

func (p *nativeQueuePlatform) CancelNativeQueued(_ context.Context, req platforms.CancelNativeQueuedRequest) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled = append(p.cancelled, req)
	return p.cancelErr
}

func (p *nativeQueuePlatform) snapshot() (sends []platforms.SendMessageRequest, cancelled []platforms.CancelNativeQueuedRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append(sends, p.sends...), append(cancelled, p.cancelled...)
}

func ocmanQueued(t *testing.T, srv *Server) int {
	t.Helper()
	n, err := srv.stateDB.CountQueuedMessages(t.Context(), "fake", "s1")
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func listQueue(t *testing.T, srv *Server, query string) []queuedMessageView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/session/s1/queue"+query, nil)
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

func deleteQueued(t *testing.T, srv *Server, id string) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodDelete, "/api/session/s1/queue/"+id+"?platform=fake", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionQueueDelete(rr, req)
	return rr.Code
}

func TestEnqueueFollowUp_WithoutNativeQueueUsesOcmanQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	var sends []platforms.SendMessageRequest
	reg.Register(&fakePlatform{
		id:       "fake",
		sessions: []db.Session{mkSession("fake", "s1", "t", 1)},
		sendMessageFn: func(req platforms.SendMessageRequest) error {
			sends = append(sends, req)
			return nil
		},
		sessionDetailFn: func(id string) (*platforms.SessionDetail, error) {
			return &platforms.SessionDetail{Session: &db.Session{ID: id, Status: db.StatusBusy}}, nil
		},
	})
	if rr := postMessage(t, srv, "s1", `{"message":"later","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
	}
	if len(sends) != 0 {
		t.Fatalf("sends = %+v, want none", sends)
	}
	if n := ocmanQueued(t, srv); n != 1 {
		t.Fatalf("ocman queue = %d, want 1", n)
	}
}

func TestEnqueueFollowUp_NativeFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name             string
		listErr, sendErr error
		wantNativeSend   bool
	}{
		{"native listing unsupported", platforms.ErrUnsupported, nil, false},
		{"native send unsupported", nil, platforms.ErrUnsupported, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, reg := newSessionsTestServer(t)
			p := newNativeQueuePlatform()
			p.listErr, p.sendErr = tc.listErr, tc.sendErr
			reg.Register(p)

			if rr := postMessage(t, srv, "s1", `{"message":"later","queue":true}`); rr.Code != http.StatusNoContent {
				t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
			}
			sends, _ := p.snapshot()
			if tc.wantNativeSend != (len(sends) == 1 && sends[0].Delivery == "queue") || len(sends) > 1 {
				t.Fatalf("sends = %+v, wantNativeSend %v", sends, tc.wantNativeSend)
			}
			if n := ocmanQueued(t, srv); n != 1 {
				t.Fatalf("ocman queue = %d, want 1 (fallback)", n)
			}
			got := listQueue(t, srv, "?platform=fake")
			if len(got) != 1 || got[0].Text != "later" {
				t.Fatalf("list = %+v, want the ocman item only", got)
			}
		})
	}
}

func TestEnqueueFollowUp_NativeQueueHoldsMessage(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	reg.Register(p)
	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()

	if rr := postMessage(t, srv, "s1", `{"message":"hold me","queue":true}`); rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body)
	}
	sends, _ := p.snapshot()
	if len(sends) != 1 || sends[0].Delivery != "queue" || sends[0].Message != "hold me" || sends[0].SessionID != "s1" {
		t.Fatalf("sends = %+v, want one Delivery=queue send", sends)
	}
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("ocman queue = %d, want 0 (held natively)", n)
	}
	msgs := drainQueueUpdated(t, sub.ch)
	if len(msgs) != 1 || msgs[0].ID != "msg_native" || msgs[0].Text != "hold me" || msgs[0].CreatedAt != 42 {
		t.Fatalf("queue.updated = %+v, want the native item", msgs)
	}
}

func TestEnqueueFollowUp_NativeSendErrorSurfaces(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.sendErr = errors.New("boom")
	reg.Register(p)
	if rr := postMessage(t, srv, "s1", `{"message":"x","queue":true}`); rr.Code == http.StatusNoContent {
		t.Fatalf("status = 204, want an error for a non-fallback send failure")
	}
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("ocman queue = %d, want 0", n)
	}
}

func TestSessionQueueList_MergesNativeItems(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.listErr = platforms.ErrUnsupported // first enqueue lands in ocman's queue
	reg.Register(p)
	postMessage(t, srv, "s1", `{"message":"ocman item","queue":true}`)
	p.mu.Lock()
	p.listErr = nil
	p.held = []platforms.NativeQueuedMessage{{ID: "msg_n1", Text: "native item", HasImages: true, CreatedAt: 7}}
	p.mu.Unlock()

	for _, query := range []string{"?platform=fake", ""} {
		got := listQueue(t, srv, query)
		// Delivery order: the platform's queue drains first.
		if len(got) != 2 || got[1].Text != "ocman item" ||
			got[0] != (queuedMessageView{ID: "msg_n1", Text: "native item", HasImages: true, CreatedAt: 7}) {
			t.Fatalf("list%s = %+v, want [native item, ocman item]", query, got)
		}
	}

	// A failing native listing degrades to the ocman items.
	p.mu.Lock()
	p.listErr = errors.New("upstream down")
	p.mu.Unlock()
	if got := listQueue(t, srv, "?platform=fake"); len(got) != 1 || got[0].Text != "ocman item" {
		t.Fatalf("list with failing native queue = %+v", got)
	}
}

func TestSessionQueueDelete_Native(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.listErr = platforms.ErrUnsupported
	reg.Register(p)
	postMessage(t, srv, "s1", `{"message":"ocman item","queue":true}`)
	ocmanID := listQueue(t, srv, "?platform=fake")[0].ID

	sub, unsub := srv.broadcastHub.subscribe()
	defer unsub()
	if code := deleteQueued(t, srv, "msg_x"); code != http.StatusNoContent {
		t.Fatalf("native delete status = %d, want 204", code)
	}
	_, cancelled := p.snapshot()
	if len(cancelled) != 1 || cancelled[0] != (platforms.CancelNativeQueuedRequest{SessionID: "s1", ID: "msg_x"}) {
		t.Fatalf("cancelled = %+v", cancelled)
	}
	drainQueueUpdated(t, sub.ch)
	if n := ocmanQueued(t, srv); n != 1 {
		t.Fatalf("ocman queue = %d after native delete, want 1 (untouched)", n)
	}

	// An ocman id goes to queuesvc, never to the platform.
	if code := deleteQueued(t, srv, ocmanID); code != http.StatusNoContent {
		t.Fatalf("ocman delete status = %d, want 204", code)
	}
	if _, cancelled = p.snapshot(); len(cancelled) != 1 {
		t.Fatalf("ocman id reached CancelNativeQueued: %+v", cancelled)
	}
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("ocman queue = %d, want 0", n)
	}

	p.mu.Lock()
	p.cancelErr = errors.New("boom")
	p.mu.Unlock()
	if code := deleteQueued(t, srv, "msg_y"); code != http.StatusInternalServerError {
		t.Fatalf("failing native cancel status = %d, want 500", code)
	}
}

// Without a NativeQueue adapter a "msg_" id is just an ocman id: a
// best-effort queuesvc delete.
func TestSessionQueueDelete_MsgIDWithoutNativeQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	reg.Register(&fakePlatform{id: "fake", sessions: []db.Session{mkSession("fake", "s1", "t", 1)}})
	if code := deleteQueued(t, srv, "msg_x"); code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", code)
	}
}

// Once a follow-up sits in ocman's queue (e.g. the native send fell back
// while the instance was unreachable), a later follow-up must not jump it
// by going to the platform's queue: OpenCode delivers its inbox at the
// next idle boundary, while ocman's flush delivers its head on the same
// edge, so the newer message would reach the agent first or interleave.
func TestEnqueueFollowUp_DoesNotOvertakeOcmanQueue(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.listErr = platforms.ErrPlatformUnreachable // instance down: nothing sent yet
	reg.Register(p)
	postMessage(t, srv, "s1", `{"message":"first","queue":true}`) // falls back to ocman

	p.mu.Lock()
	p.listErr = nil // instance back
	p.mu.Unlock()
	postMessage(t, srv, "s1", `{"message":"second","queue":true}`)

	if n := ocmanQueued(t, srv); n != 2 {
		sends, _ := p.snapshot()
		t.Fatalf("ocman queue = %d, want 2 (second queued behind first); sends = %+v", n, sends)
	}
}

// A native send that fails after it may have reached the platform (a lost
// response from a remote reads as unreachable) has an unknown outcome:
// queuing it again in ocman's queue could deliver the prompt twice. It is
// surfaced to the user instead.
func TestEnqueueFollowUp_UncertainNativeSendIsNotReplayed(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	// Simulate the remote durably accepting the input, then losing the
	// gRPC response. The hub cannot tell whether admission happened.
	p.sendMessageFn = func(req platforms.SendMessageRequest) error {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.sends = append(p.sends, req)
		p.held = append(p.held, platforms.NativeQueuedMessage{ID: "msg_accepted", Text: req.Message})
		return platforms.ErrPlatformUnreachable
	}
	reg.Register(p)
	if rr := postMessage(t, srv, "s1", `{"message":"later","queue":true}`); rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want non-replayable HTTP 422 for an uncertain send", rr.Code)
	}
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("ocman queue = %d, want 0 (no replay of an uncertain send)", n)
	}
	sends, _ := p.snapshot()
	if len(sends) != 1 {
		t.Fatalf("native admissions = %d, want exactly one", len(sends))
	}
	if held, err := p.NativeQueued(t.Context(), "s1"); err != nil || len(held) != 1 {
		t.Fatalf("remote-held items = %+v, %v; original admission must remain", held, err)
	}
}

// A selection-changing head must remain held without spending its retry
// budget while older native follow-ups remain. Once they drain it sends.
func TestQueueFlush_SelectionChangeWaitsForNativeBacklog(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.listErr = platforms.ErrUnsupported
	reg.Register(p)
	postMessage(t, srv, "s1", `{"message":"C","queue":true,"agent":"plan"}`)
	p.mu.Lock()
	p.listErr = nil
	p.sendErr = platforms.ErrUnsupported
	p.held = []platforms.NativeQueuedMessage{{ID: "msg_b", Text: "B", CreatedAt: 1}}
	p.mu.Unlock()
	for range 6 {
		srv.queueSvc().Flush(t.Context(), "fake", "s1")
	}
	head, err := srv.stateDB.HeadQueuedMessage(t.Context(), "fake", "s1")
	if err != nil || head == nil || head.Attempts != 0 {
		t.Fatalf("head = %+v, %v; want held with no failures", head, err)
	}
	sends, _ := p.snapshot()
	for _, send := range sends {
		if send.Delivery != "queue" {
			t.Fatalf("head overtook backlog: %+v", send)
		}
	}
	p.mu.Lock()
	p.held = nil
	p.sendErr = nil
	p.mu.Unlock()
	srv.queueSvc().Flush(t.Context(), "fake", "s1")
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("queue = %d after native backlog drained", n)
	}
}

// A message held in ocman's queue while the platform still holds older
// follow-ups natively joins the back of the native queue on the idle
// edge instead of being sent at once (which would overtake them).
func TestQueueFlush_JoinsNativeQueueBehindOlderItems(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	p := newNativeQueuePlatform()
	p.listErr = platforms.ErrPlatformUnreachable // instance blipped: held by ocman
	reg.Register(p)
	postMessage(t, srv, "s1", `{"message":"C","queue":true}`)
	p.mu.Lock()
	p.listErr = nil
	p.held = []platforms.NativeQueuedMessage{{ID: "msg_b", Text: "B", CreatedAt: 1}}
	p.mu.Unlock()

	srv.onSessionIdle("fake", "s1")
	if err := srv.queueFlushWorker().Drain(t.Context()); err != nil {
		t.Fatal(err)
	}
	sends, _ := p.snapshot()
	if len(sends) != 1 || sends[0].Message != "C" || sends[0].Delivery != "queue" {
		t.Fatalf("sends = %+v, want C appended to the native queue", sends)
	}
	if n := ocmanQueued(t, srv); n != 0 {
		t.Fatalf("ocman queue = %d, want drained", n)
	}
}
