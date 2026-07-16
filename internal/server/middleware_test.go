package server

import (
	"bufio"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	logtest "github.com/sirupsen/logrus/hooks/test"
)

// ringHandler returns a handler that sleeps for d and writes status.
func ringHandler(d time.Duration, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if d > 0 {
			time.Sleep(d)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte("ok"))
	})
}

// TestWithRequestTiming_LogsAtDebugForFastRequests covers the common
// path: a sub-threshold request must be observable but should not
// pollute the operator's INFO log. A fast 200 should land at DEBUG.
func TestWithRequestTiming_LogsAtDebugForFastRequests(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	h := withRequestTiming(ringHandler(0, http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/stats", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}

	entry := findEntry(hook, "/api/stats")
	if entry == nil {
		t.Fatalf("expected a log entry for /api/stats; got %d entries", len(hook.AllEntries()))
	}
	if entry.Level != log.DebugLevel {
		t.Errorf("level: got %v, want %v", entry.Level, log.DebugLevel)
	}
	if got, _ := entry.Data["status"].(int); got != http.StatusOK {
		t.Errorf("status field: got %v, want 200", entry.Data["status"])
	}
	if got, _ := entry.Data["method"].(string); got != http.MethodGet {
		t.Errorf("method field: got %v, want GET", entry.Data["method"])
	}
	if _, ok := entry.Data["duration_ms"]; !ok {
		t.Errorf("missing duration_ms field; have %#v", entry.Data)
	}
}

// TestWithRequestTiming_LogsAtDebugForSlowRequests verifies slow
// requests still log at DEBUG (all "http request" lines are debug) but
// carry the extra timings field so they can be diagnosed when DEBUG is on.
func TestWithRequestTiming_LogsAtDebugForSlowRequests(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	// Wrap handler that sleeps just over the threshold. We use
	// slowRequestThreshold + a small margin so the test is robust
	// against scheduling jitter without becoming flaky-slow.
	sleep := slowRequestThreshold + 50*time.Millisecond
	h := withRequestTiming(ringHandler(sleep, http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/api/sessions", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	entry := findEntry(hook, "/api/sessions")
	if entry == nil {
		t.Fatalf("expected a log entry for /api/sessions")
	}
	if entry.Level != log.DebugLevel {
		t.Errorf("level: got %v, want %v", entry.Level, log.DebugLevel)
	}
	dur, ok := entry.Data["duration_ms"].(int64)
	if !ok {
		t.Fatalf("duration_ms missing or wrong type: %#v", entry.Data["duration_ms"])
	}
	if dur < slowRequestThreshold.Milliseconds() {
		t.Errorf("duration_ms: got %d, want >= %d", dur, slowRequestThreshold.Milliseconds())
	}
}

// TestWithRequestTiming_CapturesNon2xxStatus verifies the wrapper
// observes the *handler's* status, not the default 200 written by
// httptest.ResponseRecorder. This matters because we want operators
// to see error rates per endpoint.
func TestWithRequestTiming_CapturesNon2xxStatus(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	h := withRequestTiming(ringHandler(0, http.StatusInternalServerError))
	req := httptest.NewRequest(http.MethodPost, "/api/cost/calc", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	entry := findEntry(hook, "/api/cost/calc")
	if entry == nil {
		t.Fatalf("expected a log entry for /api/cost/calc")
	}
	if got, _ := entry.Data["status"].(int); got != http.StatusInternalServerError {
		t.Errorf("status: got %v, want 500", entry.Data["status"])
	}
}

// TestWithRequestTiming_SkipsNoise filters out high-frequency endpoints
// that would otherwise flood the log: SSE streams (long-lived) and the
// debug-log sink (recursive — it'd log itself logging itself).
func TestWithRequestTiming_SkipsNoise(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()
	prev := log.GetLevel()
	log.SetLevel(log.DebugLevel)
	defer log.SetLevel(prev)

	h := withRequestTiming(ringHandler(0, http.StatusOK))
	for _, p := range []string{"/api/session/abc/events", "/api/debug/log"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
	}

	for _, e := range hook.AllEntries() {
		if path, _ := e.Data["path"].(string); path == "/api/session/abc/events" || path == "/api/debug/log" {
			t.Errorf("expected noisy path to be skipped, got entry: %#v", e.Data)
		}
	}
}

// findEntry returns the first hook entry whose `path` field matches.
// Returns nil when no entry matches; callers fail the test on nil so
// the diagnostic shows what *was* logged.
func findEntry(hook *logtest.Hook, path string) *log.Entry {
	for _, e := range hook.AllEntries() {
		if got, _ := e.Data["path"].(string); got == path {
			return e
		}
	}
	return nil
}

// ── statusRecorder Hijacker support ──────────────────────────────────
//
// statusRecorder must expose http.Hijacker so WebSocket upgrades (the
// in-app terminal at /api/term/ws) can take over the TCP connection.
// Without this the request flowed through statusRecorder, masking the
// underlying writer's Hijacker and breaking the upgrade with a 500.

// hijackableWriter is a ResponseWriter that records that Hijack was
// called and returns a sentinel so the delegation can be asserted.
type hijackableWriter struct {
	http.ResponseWriter
	hijacked bool
}

var errSentinelHijack = errors.New("sentinel hijack")

func (h *hijackableWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, errSentinelHijack
}

func TestStatusRecorder_DelegatesHijack(t *testing.T) {
	inner := &hijackableWriter{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: inner}

	// statusRecorder must satisfy http.Hijacker.
	hj, ok := any(rec).(http.Hijacker)
	if !ok {
		t.Fatal("statusRecorder does not implement http.Hijacker")
	}
	_, _, err := hj.Hijack()
	if !inner.hijacked {
		t.Fatal("Hijack was not delegated to the embedded ResponseWriter")
	}
	if !errors.Is(err, errSentinelHijack) {
		t.Fatalf("expected sentinel error from inner writer, got %v", err)
	}
}

// ── statusRecorder Flusher support ───────────────────────────────────
//
// statusRecorder must expose http.Flusher so SSE handlers that flow
// through the middleware (the global /api/events stream — NOT bypassed
// like /api/session/{id}/events) can stream. Without it the handler's
// `w.(http.Flusher)` check fails, no client subscribes, and no broadcast
// (e.g. ocman.queue.updated) is ever delivered — the live queue never
// updates.

type flushableWriter struct {
	http.ResponseWriter
	flushed bool
}

func (f *flushableWriter) Flush() { f.flushed = true }

func TestStatusRecorder_DelegatesFlush(t *testing.T) {
	inner := &flushableWriter{ResponseWriter: httptest.NewRecorder()}
	rec := &statusRecorder{ResponseWriter: inner}

	fl, ok := any(rec).(http.Flusher)
	if !ok {
		t.Fatal("statusRecorder does not implement http.Flusher")
	}
	fl.Flush()
	if !inner.flushed {
		t.Fatal("Flush was not delegated to the embedded ResponseWriter")
	}
}

func TestStatusRecorder_FlushUnsupported(t *testing.T) {
	// A writer without Flush must not panic — Flush is a no-op.
	type plainWriter struct{ http.ResponseWriter }
	rec := &statusRecorder{ResponseWriter: plainWriter{httptest.NewRecorder()}}
	rec.Flush() // must not panic
}

func TestStatusRecorder_HijackUnsupported(t *testing.T) {
	// httptest.ResponseRecorder does NOT implement http.Hijacker, so the
	// recorder must return a clear error rather than panicking.
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	if _, _, err := rec.Hijack(); err == nil {
		t.Fatal("expected an error when the underlying writer can't hijack")
	}
}

func TestWithSecurityHeaders(t *testing.T) {
	h := withSecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Content-Security-Policy": "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob: http: https:; connect-src 'self' ws: wss:; font-src 'self' data:; worker-src 'self' blob:; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'; manifest-src 'self'",
		"X-Frame-Options":         "DENY",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "no-referrer",
	}
	for name, value := range want {
		if got := rr.Header().Get(name); got != value {
			t.Errorf("%s: got %q, want %q", name, got, value)
		}
	}
}
