package opencode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// TestGetJSONCached_HitsUpstreamOnceAcrossCalls is the integration
// guard that the cache wiring is correct: two calls with the same
// (port, path) must produce exactly one upstream HTTP request.
//
// The catalog cache is a process-wide var, so we put a unique path
// on each test to avoid bleeding state from other tests in the
// package; we also run on a server-supplied port that no other test
// reuses.
func TestGetJSONCached_HitsUpstreamOnceAcrossCalls(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	const path = "/test_getjsoncached_unique_1"

	// First call: cold miss, should hit upstream.
	body, err := getJSONCached(context.Background(), port, path)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("first call: err=%v body=%q", err, string(body))
	}
	// Second call: warm hit, must not hit upstream.
	body, err = getJSONCached(context.Background(), port, path)
	if err != nil || string(body) != `{"ok":true}` {
		t.Fatalf("second call: err=%v body=%q", err, string(body))
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hit %d times, want 1 (cache miss → fetch → hit)", got)
	}
}

// TestGetJSONCached_FailureNotCached confirms the failure-mode
// contract end-to-end: a 500 response does NOT poison the cache, so
// the next call retries upstream rather than seeing a 30s "stuck on
// fail" window.
func TestGetJSONCached_FailureNotCached(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	const path = "/test_getjsoncached_unique_2"

	if _, err := getJSONCached(context.Background(), port, path); err == nil {
		t.Error("first call: expected an error on 500")
	} else if !strings.Contains(err.Error(), "500") {
		t.Errorf("first call: error should name the status, got %v", err)
	}
	if _, err := getJSONCached(context.Background(), port, path); err == nil {
		t.Error("second call: expected an error on 500 (failure not cached)")
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("upstream hit %d times, want 2 (each call should retry)", got)
	}
}
