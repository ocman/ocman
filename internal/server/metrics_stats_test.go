package server

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// collectStatsMetrics registers the stats gauges on a test MeterProvider,
// triggers a collection, and returns the collected metrics keyed by name.
func collectStatsMetrics(t *testing.T, srv *Server) map[string]metricdata.Metrics {
	t.Helper()

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })

	reg, err := srv.registerStatsMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("registerStatsMetrics: %v", err)
	}
	if reg == nil {
		t.Fatal("expected non-nil registration")
	}

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

func TestStatsMetrics_WithDB(t *testing.T) {
	srv := testServer(t)
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
		m, ok := metrics[name]
		if !ok {
			continue
		}
		gauge, ok := m.Data.(metricdata.Gauge[int64])
		if ok {
			for _, dp := range gauge.DataPoints {
				if dp.Value != 0 {
					t.Errorf("%s: expected 0, got %d", name, dp.Value)
				}
			}
			continue
		}
		fgauge, ok := m.Data.(metricdata.Gauge[float64])
		if ok {
			for _, dp := range fgauge.DataPoints {
				if dp.Value != 0 {
					t.Errorf("%s: expected 0, got %f", name, dp.Value)
				}
			}
			continue
		}
		t.Errorf("%s: unexpected data type %T", name, m.Data)
	}
}

func TestStatsMetrics_NilDB(t *testing.T) {
	// When the OpenCode DB is nil (e.g. only claude-code enabled),
	// registration should succeed but the callback should be a no-op.
	srv := &Server{}

	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })

	reg, err := srv.registerStatsMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("registerStatsMetrics: %v", err)
	}
	if reg != nil {
		t.Fatal("expected nil registration when db is nil")
	}
}
