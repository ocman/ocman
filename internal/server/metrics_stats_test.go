package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/NoUseFreak/ocman/internal/db"
)

// collectStatsMetrics registers the stats gauges on a test MeterProvider,
// triggers a collection, and returns the collected metrics keyed by name.
func collectStatsMetrics(t *testing.T, srv *Server) map[string]metricdata.Metrics {
	t.Helper()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // collect only; these tests refresh the snapshot explicitly
	reg, err := srv.registerStatsMetrics(ctx, provider.Meter("test"))
	if err != nil {
		t.Fatalf("registerStatsMetrics: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registration")
	}
	t.Cleanup(func() { _ = reg.Unregister() })

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}

	result := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			result[m.Name] = m
		}
	}
	return result
}

// gaugeValue returns the single data-point value of the named gauge.
func gaugeValue(t *testing.T, metrics map[string]metricdata.Metrics, name string) float64 {
	t.Helper()
	m, ok := metrics[name]
	if !ok {
		t.Fatalf("metric %q not collected", name)
	}
	switch gauge := m.Data.(type) {
	case metricdata.Gauge[int64]:
		if len(gauge.DataPoints) != 1 {
			t.Fatalf("%s: %d data points, want 1", name, len(gauge.DataPoints))
		}
		return float64(gauge.DataPoints[0].Value)
	case metricdata.Gauge[float64]:
		if len(gauge.DataPoints) != 1 {
			t.Fatalf("%s: %d data points, want 1", name, len(gauge.DataPoints))
		}
		return gauge.DataPoints[0].Value
	}
	t.Fatalf("%s: unexpected data type %T", name, m.Data)
	return 0
}

func TestStatsMetrics_WithDB(t *testing.T) {
	srv := testServer(t)
	srv.refreshStats(context.Background())
	metrics := collectStatsMetrics(t, srv)

	// All six gauges should be present.
	expected := []string{
		"ocman.stats.sessions",
		"ocman.stats.messages",
		"ocman.stats.projects",
		"ocman.stats.tokens.input",
		"ocman.stats.tokens.output",
		"ocman.stats.cost",
	}
	for _, name := range expected {
		if _, ok := metrics[name]; !ok {
			t.Errorf("expected metric %q to be present", name)
		}
	}

	// With an empty DB all values should be zero.
	for _, name := range expected {
		if v := gaugeValue(t, metrics, name); v != 0 {
			t.Errorf("%s: expected 0, got %v", name, v)
		}
	}
}

func TestStatsMetrics_NilDB(t *testing.T) {
	// When the OpenCode DB is nil (e.g. only claude-code enabled),
	// registration should succeed but the callback should be a no-op.
	srv := &Server{}

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })

	reg, err := srv.registerStatsMetrics(t.Context(), provider.Meter("test"))
	if err != nil {
		t.Fatalf("registerStatsMetrics: %v", err)
	}
	if reg != nil {
		t.Fatal("expected nil registration when db is nil")
	}
}

// TestStatsMetricsCallbackDoesNotQueryDB: the gauge callback must only read
// the cached snapshot. With the OpenCode DB closed after the first refresh,
// collections must still report the computed values, proving the export path
// no longer runs GetStats (finding: 120 mirror syncs per hour came from here).
func TestStatsMetricsCallbackDoesNotQueryDB(t *testing.T) {
	srv := testServer(t)
	var calls atomic.Int64
	srv.getStats = func(context.Context) (*db.Stats, error) {
		calls.Add(1)
		return &db.Stats{
			TotalSessions: 7, TotalMessages: 11, TotalProjects: 3,
			TotalTokensIn: 100, TotalTokensOut: 50, TotalCost: 1.5,
		}, nil
	}
	srv.refreshStats(context.Background())
	if got := calls.Load(); got != 1 {
		t.Fatalf("refresh calls = %d, want 1", got)
	}
	if err := srv.db.Close(); err != nil {
		t.Fatal(err)
	}

	for range 3 {
		metrics := collectStatsMetrics(t, srv)
		if got := gaugeValue(t, metrics, "ocman.stats.sessions"); got != 7 {
			t.Errorf("sessions = %v, want 7", got)
		}
		if got := gaugeValue(t, metrics, "ocman.stats.messages"); got != 11 {
			t.Errorf("messages = %v, want 11", got)
		}
		if got := gaugeValue(t, metrics, "ocman.stats.projects"); got != 3 {
			t.Errorf("projects = %v, want 3", got)
		}
		if got := gaugeValue(t, metrics, "ocman.stats.tokens.input"); got != 100 {
			t.Errorf("tokens.input = %v, want 100", got)
		}
		if got := gaugeValue(t, metrics, "ocman.stats.tokens.output"); got != 50 {
			t.Errorf("tokens.output = %v, want 50", got)
		}
		if got := gaugeValue(t, metrics, "ocman.stats.cost"); got != 1.5 {
			t.Errorf("cost = %v, want 1.5", got)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("collections reached the stats source: calls = %d, want 1", got)
	}
}

// TestStatsMetricsObservesNothingBeforeFirstSuccess: until the first
// successful computation the gauges report nothing.
func TestStatsMetricsObservesNothingBeforeFirstSuccess(t *testing.T) {
	srv := testServer(t)
	srv.getStats = func(context.Context) (*db.Stats, error) {
		return nil, errors.New("stats source down")
	}
	srv.refreshStats(context.Background())

	metrics := collectStatsMetrics(t, srv)
	if len(metrics) != 0 {
		t.Fatalf("expected no gauges before the first success, got %d", len(metrics))
	}

	srv.getStats = func(context.Context) (*db.Stats, error) {
		return &db.Stats{TotalSessions: 4}, nil
	}
	srv.refreshStats(context.Background())
	if got := gaugeValue(t, collectStatsMetrics(t, srv), "ocman.stats.sessions"); got != 4 {
		t.Errorf("sessions = %v, want 4", got)
	}
}

// TestStatsMetricsRefreshCadence: the loop recomputes on the injected
// interval, keeps last good values across a failure, and returns when the
// server context is cancelled (no goroutine left behind).
func TestStatsMetricsRefreshCadence(t *testing.T) {
	srv := testServer(t)
	var computed atomic.Int64
	var failing atomic.Bool
	failed := make(chan struct{}, 1)
	srv.getStats = func(context.Context) (*db.Stats, error) {
		if failing.Load() {
			select {
			case failed <- struct{}{}:
			default:
			}
			return nil, errors.New("stats source down")
		}
		return &db.Stats{TotalSessions: int(computed.Add(1))}, nil
	}
	srv.statsRefreshEvery = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })
	reg, err := srv.registerStatsMetrics(ctx, provider.Meter("test"))
	if err != nil || reg == nil {
		t.Fatalf("registerStatsMetrics: reg=%v err=%v", reg, err)
	}
	t.Cleanup(func() { _ = reg.Unregister() })

	deadline := time.Now().Add(5 * time.Second)
	for {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		if gauge := findGauge(rm, "ocman.stats.sessions"); gauge >= 3 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("gauge value never reached 3: the loop is not refreshing on the injected interval")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A failing computation must keep the last good values.
	failing.Store(true)
	select {
	case <-failed:
	case <-time.After(2 * time.Second):
		t.Fatal("failed refresh never ran")
	}
	var last metricdata.ResourceMetrics
	_ = reader.Collect(context.Background(), &last)
	kept := findGauge(last, "ocman.stats.sessions")
	time.Sleep(100 * time.Millisecond)
	var after metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &after); err != nil {
		t.Fatal(err)
	}
	if got := findGauge(after, "ocman.stats.sessions"); got != kept {
		t.Fatalf("value changed %v → %v across a failing refresh", kept, got)
	}
	cancel()
	select {
	case <-reg.(*statsMetricsRegistration).done:
	case <-time.After(2 * time.Second):
		t.Fatal("runStatsRefreshLoop did not return after context cancellation")
	}
}

func findGauge(rm metricdata.ResourceMetrics, name string) float64 {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name != name {
				continue
			}
			if g, ok := m.Data.(metricdata.Gauge[int64]); ok && len(g.DataPoints) == 1 {
				return float64(g.DataPoints[0].Value)
			}
		}
	}
	return -1
}

func TestStatsMetricsDisabledAndUnregister(t *testing.T) {
	srv := testServer(t)
	var calls atomic.Int64
	srv.getStats = func(ctx context.Context) (*db.Stats, error) {
		calls.Add(1)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	srv.statsRefreshEvery = time.Millisecond
	reg, err := srv.registerStatsMetrics(t.Context(), noop.NewMeterProvider().Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Unregister(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled telemetry queried stats")
	}

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	reg, err = srv.registerStatsMetrics(t.Context(), provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Unregister() })
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &rm); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("lazy refresh never started")
		}
		time.Sleep(time.Millisecond)
	}
	// Keep the first query blocked across several ticks. No second query may run.
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 1 {
		t.Fatal("refresh queries overlapped")
	}
	if err := reg.Unregister(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reg.(*statsMetricsRegistration).done:
	default:
		t.Fatal("unregister returned before the in-flight query exited")
	}
}
