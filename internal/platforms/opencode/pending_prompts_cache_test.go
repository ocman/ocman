package opencode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestFetchPromptSessionIDs_CachedAcrossCalls is the B6 contract:
// repeated calls within the cache TTL must hit upstream exactly
// once. Without the cache, every dashboard poll (every 5s) and
// every notify poll (every 10s, ×2 for favicon + bell) issues a
// fresh /permission and /question call to every running OpenCode
// instance, even though that data only changes on real prompt
// activity.
func TestFetchPromptSessionIDs_CachedAcrossCalls(t *testing.T) {
	resetPendingPromptCache()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"sessionID":"sx"}]`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	for i := 0; i < 5; i++ {
		got := fetchPromptSessionIDs(port, "/permission")
		if !got["sx"] {
			t.Fatalf("call %d: missing sx in %v", i, got)
		}
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("upstream hit %d times, want 1 (cache miss → fetch → 4 hits)", got)
	}
}

// TestFetchPromptSessionIDs_CacheExpiresAfterTTL ensures the cache
// is bounded — pending-prompt state can change in the background
// (e.g. another tool granted permissions), so we must refetch
// after the short TTL.
func TestFetchPromptSessionIDs_CacheExpiresAfterTTL(t *testing.T) {
	resetPendingPromptCache()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	// Burn through the cache TTL with a short test-only override.
	prev := pendingPromptCacheTTL
	swapPendingPromptCacheTTL(20 * time.Millisecond)
	defer swapPendingPromptCacheTTL(prev)

	_ = fetchPromptSessionIDs(port, "/permission")
	time.Sleep(40 * time.Millisecond)
	_ = fetchPromptSessionIDs(port, "/permission")

	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Errorf("upstream hit %d times, want 2 (one per side of TTL)", got)
	}
}

// TestFetchPromptSessionIDs_FailureNotCached confirms that a hung
// or unreachable upstream isn't remembered for the TTL. The next
// poll must retry — otherwise a transient upstream failure would
// freeze prompt indicators for the full cache window.
func TestFetchPromptSessionIDs_FailureNotCached(t *testing.T) {
	resetPendingPromptCache()

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-r.Context().Done() // hang until client times out
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	for i := 0; i < 3; i++ {
		got := fetchPromptSessionIDs(port, "/permission")
		if len(got) != 0 {
			t.Errorf("call %d: expected empty on timeout, got %v", i, got)
		}
	}

	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Errorf("upstream hit %d times, want 3 (failures must not be cached)", got)
	}
}
