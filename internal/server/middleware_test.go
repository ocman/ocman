package server

import (
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

// TestWithRequestTiming_LogsAtInfoForSlowRequests is the operator-
// facing signal: anything over slowRequestThreshold elevates to INFO so
// it shows up in normal production logs without DEBUG noise.
func TestWithRequestTiming_LogsAtInfoForSlowRequests(t *testing.T) {
	hook := logtest.NewGlobal()
	defer hook.Reset()

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
	if entry.Level != log.InfoLevel {
		t.Errorf("level: got %v, want %v", entry.Level, log.InfoLevel)
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
