package telemetry

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Cache instrumentation lives here so internal packages can record
// cache health without importing the server package. The instruments
// are package-level and lazily created on first use; when telemetry is
// disabled they're SDK no-ops.
//
// Three signals per cache, all tagged with a low-cardinality `cache`
// attribute (the cache's logical name, e.g. "opencode.sessions"):
//
//   - ocman.cache.lookups{cache, result}  — counter; result is "hit"
//     or "miss". Hit rate = lookups{result=hit} / lookups{cache=...}.
//   - ocman.cache.evictions{cache}        — counter; increments when a
//     bounded cache drops an entry to make room. Useful for spotting
//     thrash on capacity-bounded caches.
//   - ocman.cache.entries{cache}          — observable gauge; reads
//     the cache's current size on each metric collection cycle. The
//     gauge is reported via a per-cache callback registered by
//     RegisterCacheSizeGauge so cache mutation paths don't have to
//     remember to keep a counter in sync.
//
// The single-counter-with-result-attribute pattern (rather than two
// separate hit/miss counters) keeps the metric inventory tight in
// Mimir/Prometheus and matches the OTel convention used by
// `http.server.request.duration` with `http.response.status_code`.
var (
	cacheInstrumentsOnce sync.Once
	cacheLookups         metric.Int64Counter
	cacheEvictions       metric.Int64Counter

	// cacheSizes is a map of cache name -> a callback that returns
	// the current entry count. The observable gauge fires on every
	// metric collection cycle and walks this map.
	cacheSizesMu sync.RWMutex
	cacheSizes   = map[string]func() int64{}
)

// initCacheInstruments creates the cache instruments against the
// current global Meter on first use. Subsequent calls are no-ops —
// instruments are bound to the meter they were created with, so a
// later swap of the global MeterProvider doesn't retroactively
// re-bind them.
//
// Test code that wants to assert against a manual reader uses
// resetCacheInstrumentsForTests below to clear the once and force a
// re-bind to whatever meter is currently global.
func initCacheInstruments() {
	cacheInstrumentsOnce.Do(func() {
		meter := Meter()

		var err error
		if cacheLookups, err = meter.Int64Counter(
			"ocman.cache.lookups",
			metric.WithDescription("In-memory cache lookups, tagged with hit/miss outcome."),
			metric.WithUnit("{lookup}"),
		); err != nil {
			log.WithError(err).Warn("creating cache.lookups counter")
		}

		if cacheEvictions, err = meter.Int64Counter(
			"ocman.cache.evictions",
			metric.WithDescription("In-memory cache entries evicted to make room or due to TTL sweeps."),
			metric.WithUnit("{entry}"),
		); err != nil {
			log.WithError(err).Warn("creating cache.evictions counter")
		}

		// Single observable gauge that walks the cacheSizes registry
		// on every collection cycle. We register the callback once
		// rather than per-cache so the metric is created exactly once
		// regardless of how many caches end up registered.
		if _, err := meter.Int64ObservableGauge(
			"ocman.cache.entries",
			metric.WithDescription("In-memory cache entry count, sampled at metric collection time."),
			metric.WithUnit("{entry}"),
			metric.WithInt64Callback(observeCacheSizes),
		); err != nil {
			log.WithError(err).Warn("creating cache.entries observable gauge")
		}
	})
}

// observeCacheSizes is the callback the observable gauge invokes on
// every metric collection cycle. It walks the cacheSizes registry
// under a read lock and emits one observation per cache.
func observeCacheSizes(_ context.Context, observer metric.Int64Observer) error {
	cacheSizesMu.RLock()
	defer cacheSizesMu.RUnlock()
	for name, sizer := range cacheSizes {
		observer.Observe(sizer(), metric.WithAttributes(attribute.String("cache", name)))
	}
	return nil
}

// CacheMetrics is the per-cache facade call sites use to record
// hit/miss/eviction events. Construct one per cache (with a stable
// name) at cache-creation time and stash it in the cache struct.
//
// The zero value (empty Name) is a no-op so callers can test with an
// unset CacheMetrics{} without nil checks.
type CacheMetrics struct {
	// Name is the value emitted as the `cache` attribute. Pick a
	// dotted path that's unique within ocman, e.g. "opencode.sessions"
	// or "claudecode.live".
	Name string
}

// resultHit / resultMiss are pre-allocated to avoid building a new
// attribute on every lookup. The cache name varies per call so we
// still allocate per-call for that one, but the result attribute is
// fixed and worth caching.
var (
	resultHit  = attribute.String("result", "hit")
	resultMiss = attribute.String("result", "miss")
)

// RecordHit increments the lookups counter with result=hit. ctx may
// be a background context — the metrics SDK doesn't require an active
// span.
func (m CacheMetrics) RecordHit(ctx context.Context) {
	if m.Name == "" {
		return
	}
	initCacheInstruments()
	if cacheLookups == nil {
		return
	}
	cacheLookups.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache", m.Name),
		resultHit,
	))
}

// RecordMiss increments the lookups counter with result=miss.
func (m CacheMetrics) RecordMiss(ctx context.Context) {
	if m.Name == "" {
		return
	}
	initCacheInstruments()
	if cacheLookups == nil {
		return
	}
	cacheLookups.Add(ctx, 1, metric.WithAttributes(
		attribute.String("cache", m.Name),
		resultMiss,
	))
}

// RecordEvictions increments the evictions counter by n. Callers pass
// n>1 when a single sweep drops multiple entries (e.g. invalidatePort
// on the http cache). n<=0 is a no-op.
func (m CacheMetrics) RecordEvictions(ctx context.Context, n int64) {
	if m.Name == "" || n <= 0 {
		return
	}
	initCacheInstruments()
	if cacheEvictions == nil {
		return
	}
	cacheEvictions.Add(ctx, n, metric.WithAttributes(
		attribute.String("cache", m.Name),
	))
}

// RegisterCacheSizeGauge wires a per-cache size callback into the
// shared observable gauge. Call once at cache construction; the
// supplied function is invoked on every metric collection cycle and
// must be safe to call from a goroutine other than the cache's
// usual writers.
//
// Re-registering a cache name overwrites the previous callback,
// which makes test setup simpler (each test can replace the cache
// without colliding with a leftover registration). Calling this also
// triggers instrument creation, so a cache that only registers a
// size gauge (no lookups) still appears in the metric stream.
func RegisterCacheSizeGauge(name string, sizer func() int64) {
	if name == "" || sizer == nil {
		return
	}
	initCacheInstruments()
	cacheSizesMu.Lock()
	cacheSizes[name] = sizer
	cacheSizesMu.Unlock()
}

// UnregisterCacheSizeGauge drops a cache from the size-gauge
// registry. Mostly useful for tests; production caches are
// process-lived so they're never unregistered.
func UnregisterCacheSizeGauge(name string) {
	cacheSizesMu.Lock()
	delete(cacheSizes, name)
	cacheSizesMu.Unlock()
}

// resetCacheInstrumentsForTests forces re-binding of the cache
// instruments to the current global Meter on the next call. Tests
// that swap the global MeterProvider (e.g. to install a
// ManualReader) must call this so subsequent Record* calls land on
// the test meter rather than the original (no-op or production)
// meter.
//
// Not exported — telemetry is the only package allowed to look
// inside its own once-state.
func resetCacheInstrumentsForTests() {
	cacheInstrumentsOnce = sync.Once{}
	cacheLookups = nil
	cacheEvictions = nil
	cacheSizesMu.Lock()
	cacheSizes = map[string]func() int64{}
	cacheSizesMu.Unlock()
}
