package opencode

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/NoUseFreak/ocman/internal/telemetry"
)

// httpCache is a small TTL + singleflight cache for upstream OpenCode
// responses keyed by (port, path). It exists to absorb the "click on
// a session" thundering-herd of GETs for /agent, /command, and
// /provider — each of which served stale-friendly catalog data that
// changes only when the user edits config.
//
// Design notes:
//   - Stores raw response bytes. We don't parse here so the same
//     cache can serve any JSON endpoint without generic wrangling.
//   - One TTL per cache instance. If we later need different TTLs
//     per endpoint, we'll switch the value type to carry it; for now
//     a single 30s TTL is plenty.
//   - Failures are NOT cached (see TestHTTPCache_GetOrFetchDoesNotCacheFailures).
//     A "no running OpenCode" miss should retry on the next call, not
//     be remembered for 30s.
//   - getOrFetch wraps fetches in a singleflight.Group keyed by
//     "port|path" so concurrent misses for the same key only fire one
//     upstream call. This is the key win on a cold SessionDetail
//     mount, which fires multiple endpoints simultaneously.
type httpCache struct {
	ttl time.Duration

	mu      sync.RWMutex
	entries map[httpCacheKey]httpCacheEntry

	flight singleflight.Group

	// metrics is the per-cache instrumentation handle. Zero value
	// (empty Name) makes every Record* call a no-op so the cache
	// works fine in tests that construct it via newHTTPCache without
	// a name.
	metrics telemetry.CacheMetrics
}

type httpCacheKey struct {
	port string
	path string
}

type httpCacheEntry struct {
	body      []byte
	expiresAt time.Time
}

func newHTTPCache(ttl time.Duration) *httpCache {
	return &httpCache{
		ttl:     ttl,
		entries: make(map[httpCacheKey]httpCacheEntry),
	}
}

// newHTTPCacheNamed is newHTTPCache plus telemetry: it tags every
// hit/miss/eviction with the supplied name and registers a
// process-wide observable gauge that reports the current entry count.
//
// Production caches use this constructor so we can chart hit rate
// and growth in Grafana; tests keep using newHTTPCache to stay
// unaffected by the global metric registry.
func newHTTPCacheNamed(ttl time.Duration, name string) *httpCache {
	c := newHTTPCache(ttl)
	c.metrics = telemetry.CacheMetrics{Name: name}
	telemetry.RegisterCacheSizeGauge(name, func() int64 {
		c.mu.RLock()
		defer c.mu.RUnlock()
		return int64(len(c.entries))
	})
	return c
}

// get returns the cached body for (port, path) iff a non-expired
// entry exists. The returned slice is the cached buffer — callers
// must not mutate it. (None of our call sites do; they all hand the
// bytes to json.Unmarshal which copies what it needs.)
func (c *httpCache) get(port, path string) ([]byte, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[httpCacheKey{port, path}]
	if !ok {
		return nil, false
	}
	if time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.body, true
}

// put stores a successful response. Overwrites any existing entry for
// the same key.
func (c *httpCache) put(port, path string, body []byte) {
	c.mu.Lock()
	c.entries[httpCacheKey{port, path}] = httpCacheEntry{
		body:      body,
		expiresAt: time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

// invalidate drops a single (port, path) entry. Useful after a
// mutating op that would change the upstream response. Currently
// unused; provided for future call sites.
func (c *httpCache) invalidate(port, path string) {
	c.mu.Lock()
	_, existed := c.entries[httpCacheKey{port, path}]
	delete(c.entries, httpCacheKey{port, path})
	c.mu.Unlock()
	if existed {
		c.metrics.RecordEvictions(context.Background(), 1)
	}
}

// invalidatePort drops every entry for a port. Called when port
// discovery observes that a previously-running OpenCode instance has
// disappeared, so we don't keep its cached catalog around forever.
func (c *httpCache) invalidatePort(port string) {
	c.mu.Lock()
	var dropped int64
	for k := range c.entries {
		if k.port == port {
			delete(c.entries, k)
			dropped++
		}
	}
	c.mu.Unlock()
	c.metrics.RecordEvictions(context.Background(), dropped)
}

// getOrFetch returns the cached body if present, otherwise calls
// fetcher (singleflighted on the cache key) and stores the result on
// success. On failure (fetcher returns ok=false) the result is NOT
// cached, so callers retry on the next call.
//
// fetcher is a closure rather than a method receiver / interface so
// each call site can capture exactly the URL + parsing it needs
// without us building yet another HTTP-call abstraction.
//
// Hit/miss telemetry is recorded once per top-level call: a hit on
// the fast path, a miss otherwise. The second-check inside the
// singleflight body is intentionally not double-counted — it's an
// implementation detail of the coalescing strategy, not a real
// lookup. Counting it would inflate the hit-rate denominator under
// concurrent traffic and obscure the actual behaviour of the cache.
func (c *httpCache) getOrFetch(port, path string, fetcher func() ([]byte, bool)) ([]byte, bool) {
	ctx := context.Background()
	if body, ok := c.get(port, path); ok {
		c.metrics.RecordHit(ctx)
		return body, true
	}
	c.metrics.RecordMiss(ctx)

	key := port + "|" + path
	v, err, _ := c.flight.Do(key, func() (interface{}, error) {
		// Re-check inside the singleflight body in case another
		// caller filled the cache between our miss and acquiring
		// the flight slot. Cheap insurance.
		if body, ok := c.get(port, path); ok {
			return body, nil
		}
		body, ok := fetcher()
		if !ok {
			// Sentinel: returning an error makes singleflight
			// surface failure to all sharers, AND we explicitly
			// don't put() so the next call retries. We still
			// distinguish "fetcher failed" from "cache hit" via
			// the error value.
			return nil, errFetchFailed
		}
		c.put(port, path, body)
		return body, nil
	})
	if err != nil {
		return nil, false
	}
	body, _ := v.([]byte)
	return body, body != nil
}

// errFetchFailed is the sentinel singleflight error used to signal
// "fetcher returned ok=false". It's not exported because callers see
// only the (body, ok) shape.
var errFetchFailed = errFetchFailedT{}

type errFetchFailedT struct{}

func (errFetchFailedT) Error() string { return "opencode: upstream fetch failed" }
