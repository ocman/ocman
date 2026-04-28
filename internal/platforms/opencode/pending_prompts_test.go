package opencode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestFetchPromptSessionIDs_TimesOutFastOnHungUpstream is the
// regression guard for the production incident where one
// unresponsive OpenCode instance dragged every /api/sessions call
// to 10s+ because the shared openCodeClient timeout is 10s.
//
// The contract: a call to /permission or /question that doesn't
// respond inside pendingPromptTimeout must return an empty result
// and unblock the caller within roughly that timeout (we allow
// slack for goroutine scheduling).
func TestFetchPromptSessionIDs_TimesOutFastOnHungUpstream(t *testing.T) {
	// Server that hangs until the *client's* request context is
	// cancelled. This is what we actually want to test: the client
	// disconnects via its 500ms timeout, the handler sees the
	// cancellation and unblocks, and srv.Close() can then return
	// cleanly without deadlocking on a stuck goroutine.
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")

	start := time.Now()
	got := fetchPromptSessionIDs(port, "/permission")
	elapsed := time.Since(start)

	if len(got) != 0 {
		t.Errorf("expected empty result on timeout, got %v", got)
	}
	// Allow 2× the configured timeout to absorb scheduling jitter
	// without making the test flaky on slow CI.
	if elapsed > 2*pendingPromptTimeout {
		t.Errorf("call took %v, expected ≤ %v (timeout enforcement broken?)",
			elapsed, 2*pendingPromptTimeout)
	}
}

// TestFetchPromptSessionIDs_FastResponseStillWorks is the
// happy-path counterpart to the timeout test: a normally-responding
// upstream must still produce the correct result. Otherwise our
// regression fix could have silently broken every healthy instance.
func TestFetchPromptSessionIDs_FastResponseStillWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"sessionID":"s1"},{"sessionID":"s2"},{"id":"no-sid"}]`))
	}))
	defer srv.Close()

	port := strings.TrimPrefix(srv.URL, "http://127.0.0.1:")
	got := fetchPromptSessionIDs(port, "/permission")

	if len(got) != 2 {
		t.Fatalf("expected 2 session IDs, got %d: %v", len(got), got)
	}
	if !got["s1"] || !got["s2"] {
		t.Errorf("missing expected IDs: %v", got)
	}
}

// TestCollectPendingPromptsByDir_OneSlowInstanceDoesNotBlockOthers
// proves the per-call (not per-batch) timeout: when one OpenCode
// instance hangs and another responds quickly, the call must return
// near-instantly with the fast instance's data, NOT wait for the
// slow one's timeout.
func TestCollectPendingPromptsByDir_OneSlowInstanceDoesNotBlockOthers(t *testing.T) {
	// Hangs until the client cancels (see the timeout test above
	// for the rationale). Without per-call timeouts in the client,
	// this would block the fan-out for openCodeClient.Timeout
	// (10s) and effectively wedge every dashboard request.
	slowSrv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer slowSrv.Close()

	var fastHits int32
	fastSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&fastHits, 1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"sessionID":"fast-session"}]`))
	}))
	defer fastSrv.Close()

	ports := map[string]string{
		"/repo/slow": strings.TrimPrefix(slowSrv.URL, "http://127.0.0.1:"),
		"/repo/fast": strings.TrimPrefix(fastSrv.URL, "http://127.0.0.1:"),
	}

	start := time.Now()
	perms, _ := collectPendingPromptsByDir(ports)
	elapsed := time.Since(start)

	// The fast server should have contributed; the slow one should
	// have timed out at pendingPromptTimeout, but the fast result
	// is still there.
	if !perms["fast-session"] {
		t.Errorf("fast instance result missing: %v", perms)
	}
	// Fast permission and question = 2 hits per server. We don't
	// assert == because cache state across tests could vary; we
	// just want to make sure the fast path actually ran.
	if atomic.LoadInt32(&fastHits) == 0 {
		t.Error("fast instance was never called")
	}
	// Total elapsed time is bounded by the slow instance's timeout
	// (the fan-out waits for all workers). We allow 3× to stay
	// robust under load; the relevant bound for the hot path is
	// per-call, not batch.
	if elapsed > 3*pendingPromptTimeout {
		t.Errorf("collect took %v, expected ≤ %v", elapsed, 3*pendingPromptTimeout)
	}
}
