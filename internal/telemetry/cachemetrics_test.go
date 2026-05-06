package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// withTestMeterProvider installs a fresh SDK MeterProvider with a
// ManualReader as the global, resets the cache-instrument once-state
// so the next Record* call binds to it, and returns the reader for
// collecting. The previous global is restored on cleanup.
func withTestMeterProvider(t *testing.T) *sdkmetric.ManualReader {
	t.Helper()

	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)

	resetCacheInstrumentsForTests()

	t.Cleanup(func() {
		otel.SetMeterProvider(prev)
		resetCacheInstrumentsForTests()
		_ = provider.Shutdown(context.Background())
	})
	return reader
}

// collectMetrics drains the manual reader and returns the captured
// metrics keyed by name for ergonomic assertions.
func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	out := map[string]metricdata.Metrics{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out[m.Name] = m
		}
	}
	return out
}

// findInt64SumDataPoint returns the value for the data point whose
// attribute set matches every requested key=value, or fails the test.
func findInt64SumDataPoint(t *testing.T, m metricdata.Metrics, want map[string]string) int64 {
	t.Helper()

	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("metric %q is not a Sum[int64], got %T", m.Name, m.Data)
	}
	for _, dp := range sum.DataPoints {
		if attrSetMatches(dp.Attributes, want) {
			return dp.Value
		}
	}
	t.Fatalf("metric %q has no data point matching %v; saw %v", m.Name, want, sum.DataPoints)
	return 0
}

// findInt64GaugeDataPoint returns the value for the gauge data point
// whose attribute set matches every requested key=value, or fails.
func findInt64GaugeDataPoint(t *testing.T, m metricdata.Metrics, want map[string]string) int64 {
	t.Helper()

	gauge, ok := m.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("metric %q is not a Gauge[int64], got %T", m.Name, m.Data)
	}
	for _, dp := range gauge.DataPoints {
		if attrSetMatches(dp.Attributes, want) {
			return dp.Value
		}
	}
	t.Fatalf("metric %q has no data point matching %v; saw %v", m.Name, want, gauge.DataPoints)
	return 0
}

func attrSetMatches(set attribute.Set, want map[string]string) bool {
	for k, v := range want {
		got, ok := set.Value(attribute.Key(k))
		if !ok || got.AsString() != v {
			return false
		}
	}
	return true
}

func TestCacheMetrics_RecordHitMiss(t *testing.T) {
	reader := withTestMeterProvider(t)
	m := CacheMetrics{Name: "test.cache"}
	ctx := context.Background()

	m.RecordHit(ctx)
	m.RecordHit(ctx)
	m.RecordHit(ctx)
	m.RecordMiss(ctx)

	metrics := collectMetrics(t, reader)
	lookups, ok := metrics["ocman.cache.lookups"]
	if !ok {
		t.Fatalf("expected ocman.cache.lookups metric; got %v", keys(metrics))
	}

	hits := findInt64SumDataPoint(t, lookups, map[string]string{"cache": "test.cache", "result": "hit"})
	if hits != 3 {
		t.Errorf("hits = %d, want 3", hits)
	}
	misses := findInt64SumDataPoint(t, lookups, map[string]string{"cache": "test.cache", "result": "miss"})
	if misses != 1 {
		t.Errorf("misses = %d, want 1", misses)
	}
}

func TestCacheMetrics_RecordEvictions(t *testing.T) {
	reader := withTestMeterProvider(t)
	m := CacheMetrics{Name: "test.cache"}
	ctx := context.Background()

	m.RecordEvictions(ctx, 5)
	m.RecordEvictions(ctx, 2)
	m.RecordEvictions(ctx, 0) // no-op
	m.RecordEvictions(ctx, -1) // no-op

	metrics := collectMetrics(t, reader)
	evictions, ok := metrics["ocman.cache.evictions"]
	if !ok {
		t.Fatalf("expected ocman.cache.evictions metric; got %v", keys(metrics))
	}
	v := findInt64SumDataPoint(t, evictions, map[string]string{"cache": "test.cache"})
	if v != 7 {
		t.Errorf("evictions = %d, want 7", v)
	}
}

func TestCacheMetrics_DistinctNamesAreSeparateSeries(t *testing.T) {
	reader := withTestMeterProvider(t)
	a := CacheMetrics{Name: "cache.a"}
	b := CacheMetrics{Name: "cache.b"}
	ctx := context.Background()

	a.RecordHit(ctx)
	a.RecordHit(ctx)
	b.RecordMiss(ctx)

	metrics := collectMetrics(t, reader)
	lookups := metrics["ocman.cache.lookups"]

	if v := findInt64SumDataPoint(t, lookups, map[string]string{"cache": "cache.a", "result": "hit"}); v != 2 {
		t.Errorf("cache.a hits = %d, want 2", v)
	}
	if v := findInt64SumDataPoint(t, lookups, map[string]string{"cache": "cache.b", "result": "miss"}); v != 1 {
		t.Errorf("cache.b misses = %d, want 1", v)
	}
}

func TestCacheMetrics_EmptyNameIsNoOp(t *testing.T) {
	reader := withTestMeterProvider(t)
	var m CacheMetrics // zero value, Name == ""
	ctx := context.Background()

	m.RecordHit(ctx)
	m.RecordMiss(ctx)
	m.RecordEvictions(ctx, 10)

	metrics := collectMetrics(t, reader)
	// No instruments created -> the SDK collects zero scope metrics
	// for our scope. Lookups/evictions metrics may exist (instruments
	// are created lazily on first non-empty-name call from another
	// test) but should have zero data points for this run.
	if lookups, ok := metrics["ocman.cache.lookups"]; ok {
		if sum, ok := lookups.Data.(metricdata.Sum[int64]); ok && len(sum.DataPoints) > 0 {
			t.Errorf("expected no lookup data points, got %v", sum.DataPoints)
		}
	}
}

func TestRegisterCacheSizeGauge(t *testing.T) {
	reader := withTestMeterProvider(t)

	var size int64 = 42
	RegisterCacheSizeGauge("sized.cache", func() int64 { return size })
	t.Cleanup(func() { UnregisterCacheSizeGauge("sized.cache") })

	metrics := collectMetrics(t, reader)
	entries, ok := metrics["ocman.cache.entries"]
	if !ok {
		t.Fatalf("expected ocman.cache.entries metric; got %v", keys(metrics))
	}
	if v := findInt64GaugeDataPoint(t, entries, map[string]string{"cache": "sized.cache"}); v != 42 {
		t.Errorf("size = %d, want 42", v)
	}

	// Mutate the underlying value and re-collect — the gauge should
	// reflect the new size on the next collection cycle.
	size = 100
	metrics = collectMetrics(t, reader)
	if v := findInt64GaugeDataPoint(t, metrics["ocman.cache.entries"], map[string]string{"cache": "sized.cache"}); v != 100 {
		t.Errorf("size after mutation = %d, want 100", v)
	}
}

func TestRegisterCacheSizeGauge_OverwritesPriorRegistration(t *testing.T) {
	reader := withTestMeterProvider(t)

	RegisterCacheSizeGauge("name", func() int64 { return 1 })
	RegisterCacheSizeGauge("name", func() int64 { return 99 })
	t.Cleanup(func() { UnregisterCacheSizeGauge("name") })

	metrics := collectMetrics(t, reader)
	if v := findInt64GaugeDataPoint(t, metrics["ocman.cache.entries"], map[string]string{"cache": "name"}); v != 99 {
		t.Errorf("size = %d, want 99 (latest registration should win)", v)
	}
}

func TestRegisterCacheSizeGauge_IgnoresEmptyName(t *testing.T) {
	withTestMeterProvider(t)

	// Should not panic, should not register anything.
	RegisterCacheSizeGauge("", func() int64 { return 1 })
	RegisterCacheSizeGauge("name", nil)

	cacheSizesMu.RLock()
	n := len(cacheSizes)
	cacheSizesMu.RUnlock()
	if n != 0 {
		t.Errorf("expected 0 registered sizers, got %d", n)
	}
}

func keys(m map[string]metricdata.Metrics) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
