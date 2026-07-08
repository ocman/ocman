package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

// TestSessionEvents_PlatformUnreachableReturns503 reproduces the
// post-reboot freeze where no OpenCode instance is running for the
// session's directory. Before the fix, serveSessionEvents wrote the
// SSE headers, ProxyEvents returned ErrPlatformUnreachable before
// emitting any bytes, and the response ended as a 200 OK with an
// empty body. EventSource interpreted that as a healthy stream that
// closed cleanly and reconnected in a tight loop, eating one of the
// browser's ~6 HTTP/1.1 connection slots and starving the other API
// requests (the symptom: /agents, /models, /sessions, /git/info all
// stuck in the pending queue).
//
// The fix detects "ProxyEvents failed before producing output" via a
// lazy-header writer and returns HTTP 503 instead. Per the WHATWG
// spec, EventSource gives up on non-200 responses, freeing the socket.
func TestSessionEvents_PlatformUnreachableReturns503(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "s1", Platform: "fake"}},
		proxyEventsFn: func(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
			return fmt.Errorf("no running fake instance for session %s: %w", sessionID, platforms.ErrPlatformUnreachable)
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/session/s1/events", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionEvents(rr, req)

	if rr.Code != 503 {
		t.Fatalf("status = %d, want 503; body=%q", rr.Code, rr.Body.String())
	}
	// The Content-Type must NOT be text/event-stream — that would tell
	// the browser this is a live SSE channel and trigger a reconnect.
	if ct := rr.Header().Get("Content-Type"); strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, must not be text/event-stream on 503", ct)
	}
}

// TestSessionEvents_StreamBytesFlowProducesOk verifies the happy path
// is unaffected by the lazy-header wrapper: ProxyEvents writes bytes,
// returns nil, and the client sees 200 OK with the SSE Content-Type
// and the event bytes intact.
func TestSessionEvents_StreamBytesFlowProducesOk(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	const payload = "event: ping\ndata: {}\n\n"
	fp := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "s1", Platform: "fake"}},
		proxyEventsFn: func(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
			if _, err := io.WriteString(w, payload); err != nil {
				return err
			}
			return nil
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/session/s1/events", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionEvents(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
	if rr.Body.String() != payload {
		t.Errorf("body = %q, want %q", rr.Body.String(), payload)
	}
}

// TestSessionEvents_UnreachableAfterFirstByteStaysOk locks in the
// boundary case: if ProxyEvents successfully writes bytes and *then*
// hits an unreachable error (e.g. the OpenCode process is killed
// mid-stream), the response must stay 200 — the SSE channel was
// already established, the browser's EventSource will handle the
// drop with its own reconnect logic, and downgrading to 503
// retroactively would corrupt the response.
func TestSessionEvents_UnreachableAfterFirstByteStaysOk(t *testing.T) {
	srv, reg := newSessionsTestServer(t)
	fp := &fakePlatform{
		id:       "fake",
		sessions: []db.Session{{ID: "s1", Platform: "fake"}},
		proxyEventsFn: func(ctx context.Context, sessionID string, w io.Writer, flush func()) error {
			if _, err := io.WriteString(w, ":ok\n\n"); err != nil {
				return err
			}
			return fmt.Errorf("upstream went away: %w", platforms.ErrPlatformUnreachable)
		},
	}
	reg.Register(fp)

	req := httptest.NewRequest(http.MethodGet, "/api/session/s1/events", nil)
	rr := httptest.NewRecorder()
	srv.handleSessionEvents(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 (headers already flushed); body=%q", rr.Code, rr.Body.String())
	}
	if !strings.HasPrefix(rr.Body.String(), ":ok") {
		t.Errorf("body = %q, want it to start with the comment we wrote", rr.Body.String())
	}
}
