package opencode

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestHTTPCache_StoresAndReturns verifies the basic put/get round trip.
// Most tests in this file exercise corner cases; the happy path lives
// here as a sanity check.
func TestHTTPCache_StoresAndReturns(t *testing.T) {
	c := newHTTPCache(time.Minute)
	c.put("port1", "/agent", []byte("hello"))
	got, ok := c.get("port1", "/agent")
	if !ok {
		t.Fatal("expected hit")
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", string(got))
	}
}

// TestHTTPCache_DistinctKeys ensures a single cache instance can hold
// multiple ports and paths without collision. This matters because
// every running OpenCode instance gets its own port, and we serve any
// number of them at once.
func TestHTTPCache_DistinctKeys(t *testing.T) {
	c := newHTTPCache(time.Minute)
	c.put("port1", "/agent", []byte("a"))
	c.put("port1", "/command", []byte("b"))
	c.put("port2", "/agent", []byte("c"))

	if v, _ := c.get("port1", "/agent"); string(v) != "a" {
		t.Errorf("port1/agent: got %q", string(v))
	}
	if v, _ := c.get("port1", "/command"); string(v) != "b" {
		t.Errorf("port1/command: got %q", string(v))
	}
	if v, _ := c.get("port2", "/agent"); string(v) != "c" {
		t.Errorf("port2/agent: got %q", string(v))
	}
}

// TestHTTPCache_ExpiresAfterTTL is the eviction-by-TTL contract. We
// use a tiny TTL and a real sleep — alternative would be an injected
// clock, but the cache uses time.Now() directly and changing that
// just for the test is not worth the indirection.
func TestHTTPCache_ExpiresAfterTTL(t *testing.T) {
	c := newHTTPCache(20 * time.Millisecond)
	c.put("p", "/x", []byte("v"))
	if _, ok := c.get("p", "/x"); !ok {
		t.Fatal("expected hit immediately after put")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := c.get("p", "/x"); ok {
		t.Error("expected miss after TTL elapsed")
	}
}

// TestHTTPCache_GetOrFetchSingleflights is the headline behaviour: N
// concurrent misses for the same key should fire exactly one fetcher.
// This is what saves us from the cold-mount thundering herd.
func TestHTTPCache_GetOrFetchSingleflights(t *testing.T) {
	c := newHTTPCache(time.Minute)
	const concurrency = 20
	var fetches int32
	var wg sync.WaitGroup
	wg.Add(concurrency)

	fetcher := func() ([]byte, bool) {
		atomic.AddInt32(&fetches, 1)
		// Sleep long enough that all goroutines pile onto the same
		// in-flight call. Without singleflight, every goroutine
		// would race past the cache miss and fetch independently.
		time.Sleep(50 * time.Millisecond)
		return []byte("payload"), true
	}

	results := make([][]byte, concurrency)
	for i := 0; i < concurrency; i++ {
		go func(idx int) {
			defer wg.Done()
			b, ok := c.getOrFetch("port", "/x", fetcher)
			if !ok {
				t.Errorf("goroutine %d: expected ok", idx)
				return
			}
			results[idx] = b
		}(i)
	}
	wg.Wait()

	if got := atomic.LoadInt32(&fetches); got != 1 {
		t.Errorf("fetcher invoked %d times, want 1", got)
	}
	for i, r := range results {
		if string(r) != "payload" {
			t.Errorf("goroutine %d got %q", i, string(r))
		}
	}
}

// TestHTTPCache_GetOrFetchDoesNotCacheFailures is intentional: when
// the upstream endpoint is unreachable we don't want to remember
// "this is unreachable" for 30s. The retry-soon behaviour is more
// useful than the cost of an extra failed HTTP call.
func TestHTTPCache_GetOrFetchDoesNotCacheFailures(t *testing.T) {
	c := newHTTPCache(time.Minute)
	var calls int32

	fetcher := func() ([]byte, bool) {
		atomic.AddInt32(&calls, 1)
		return nil, false
	}

	if _, ok := c.getOrFetch("p", "/x", fetcher); ok {
		t.Error("expected ok=false when fetcher fails")
	}
	if _, ok := c.getOrFetch("p", "/x", fetcher); ok {
		t.Error("expected ok=false on retry")
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("fetcher called %d times, want 2 (no failure caching)", got)
	}
}

// TestHTTPCache_InvalidateClearsKey is the escape hatch for callers
// who know the upstream state changed (e.g. after a mutating op that
// would invalidate the catalog).
func TestHTTPCache_InvalidateClearsKey(t *testing.T) {
	c := newHTTPCache(time.Minute)
	c.put("p", "/x", []byte("v"))
	c.invalidate("p", "/x")
	if _, ok := c.get("p", "/x"); ok {
		t.Error("expected miss after invalidate")
	}
}

// TestHTTPCache_InvalidatePort drops every entry for a port. Used
// when a port disappears from the lsof scan — keeping its cached
// payloads around would just waste memory.
func TestHTTPCache_InvalidatePort(t *testing.T) {
	c := newHTTPCache(time.Minute)
	c.put("p1", "/agent", []byte("a"))
	c.put("p1", "/command", []byte("b"))
	c.put("p2", "/agent", []byte("c"))

	c.invalidatePort("p1")

	if _, ok := c.get("p1", "/agent"); ok {
		t.Error("p1/agent: expected miss")
	}
	if _, ok := c.get("p1", "/command"); ok {
		t.Error("p1/command: expected miss")
	}
	if _, ok := c.get("p2", "/agent"); !ok {
		t.Error("p2/agent: expected hit (different port)")
	}
}
