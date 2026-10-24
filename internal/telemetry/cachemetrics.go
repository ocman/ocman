package telemetry

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Cache instrumentation lives here so internal packages can record
// cache health without importing the server package. Each cache's
// counters are created by NewCacheMetrics; when telemetry is disabled
// they're SDK no-ops.
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
	cacheGaugeOnce sync.Once

	// cacheSizes is a map of cache name -> a callback that returns
	// the current entry count. The observable gauge fires on every
	// metric collection cycle and walks this map.
	cacheSizesMu sync.RWMutex
	cacheSizes   = map[string]func() int64{}
)

// initCacheGauge creates the shared entries gauge against the current
// global Meter on first use. Test code that swaps the global
// MeterProvider calls resetCacheInstrumentsForTests to re-bind it.
func initCacheGauge() {
	cacheGaugeOnce.Do(func() {
		// One observable gauge walks the cacheSizes registry on every
		// collection cycle, however many caches end up registered.
		if _, err := Meter().Int64ObservableGauge(
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

// CacheMetrics records one cache's hit/miss/eviction events. Create one
// per cache with NewCacheMetrics and stash it in the cache struct. The
// zero value is a no-op so tests can leave it unset.
type CacheMetrics struct {
	name      attribute.KeyValue
	lookups   metric.Int64Counter
	evictions metric.Int64Counter
}

// resultHit / resultMiss are pre-allocated to avoid building a new
// attribute on every lookup.
var (
	resultHit  = attribute.String("result", "hit")
	resultMiss = attribute.String("result", "miss")
)

// NewCacheMetrics creates the counters for the cache called name, the
// value emitted as the `cache` attribute. Pick a dotted path that's
// unique within ocman, e.g. "opencode.sessions". An empty name is a no-op.
func NewCacheMetrics(name string) CacheMetrics {
	if name == "" {
		return CacheMetrics{}
	}
	m := CacheMetrics{name: attribute.String("cache", name)}
	meter := Meter()
	var err error
	if m.lookups, err = meter.Int64Counter(
		"ocman.cache.lookups",
		metric.WithDescription("In-memory cache lookups, tagged with hit/miss outcome."),
		metric.WithUnit("{lookup}"),
	); err != nil {
		log.WithError(err).Warn("creating cache.lookups counter")
	}
	if m.evictions, err = meter.Int64Counter(
		"ocman.cache.evictions",
		metric.WithDescription("In-memory cache entries evicted to make room or due to TTL sweeps."),
		metric.WithUnit("{entry}"),
	); err != nil {
		log.WithError(err).Warn("creating cache.evictions counter")
	}
	return m
}

// RecordHit increments the lookups counter with result=hit. ctx may be
// a background context — the metrics SDK doesn't require an active span.
func (m CacheMetrics) RecordHit(ctx context.Context) { m.record(ctx, m.lookups, 1, resultHit) }

// RecordMiss increments the lookups counter with result=miss.
func (m CacheMetrics) RecordMiss(ctx context.Context) { m.record(ctx, m.lookups, 1, resultMiss) }

// RecordEvictions increments the evictions counter by n. Callers pass
// n>1 when a single sweep drops multiple entries (e.g. invalidatePort
// on the http cache). n<=0 is a no-op.
func (m CacheMetrics) RecordEvictions(ctx context.Context, n int64) {
	m.record(ctx, m.evictions, n)
}

func (m CacheMetrics) record(ctx context.Context, counter metric.Int64Counter, n int64, attrs ...attribute.KeyValue) {
	if counter == nil || n <= 0 {
		return
	}
	counter.Add(ctx, n, metric.WithAttributes(append(attrs, m.name)...))
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
// creates the shared gauge.
func RegisterCacheSizeGauge(name string, sizer func() int64) {
	if name == "" || sizer == nil {
		return
	}
	initCacheGauge()
	cacheSizesMu.Lock()
	cacheSizes[name] = sizer
	cacheSizesMu.Unlock()
}
