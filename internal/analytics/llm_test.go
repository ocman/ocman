package analytics

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/telemetry"
)

func TestLLMMetrics_Record(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })

	m, err := newLLMMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatalf("newLLMMetrics: %v", err)
	}

	ctx := context.Background()
	// Pass nil pricing table — calc_cost should be 0 (no pricing data).
	m.record(ctx, db.LLMMessageRow{
		Model:            "anthropic/claude-3",
		InputTokens:      100,
		OutputTokens:     50,
		CacheReadTokens:  80,
		CacheWriteTokens: 20,
		Cost:             0.005,
		StopReason:       "end_turn",
		DurationMs:       1500,
	}, nil)
	m.record(ctx, db.LLMMessageRow{
		Model:        "google/gemini",
		InputTokens:  200,
		OutputTokens: 100,
		Cost:         0.01,
		StopReason:   "error",
		DurationMs:   0, // no duration — should not record histogram
	}, nil)

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		t.Fatalf("collecting metrics: %v", err)
	}

	metrics := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metrics[m.Name] = m
		}
	}

	// Verify all instruments are present (calc_cost is absent because
	// nil pricing means no cost was computed, so the counter was never
	// incremented and OTel omits it).
	expected := []string{
		"ocman.llm.requests",
		"ocman.llm.tokens.input",
		"ocman.llm.tokens.output",
		"ocman.llm.tokens.cache_read",
		"ocman.llm.tokens.cache_write",
		"ocman.llm.cost",
		"ocman.llm.request.duration",
	}
	for _, name := range expected {
		if _, ok := metrics[name]; !ok {
			t.Errorf("expected metric %q to be present", name)
		}
	}

	// Verify counter totals.
	assertInt64Sum(t, metrics, "ocman.llm.requests", 2)
	assertInt64Sum(t, metrics, "ocman.llm.tokens.input", 300)
	assertInt64Sum(t, metrics, "ocman.llm.tokens.output", 150)
	assertInt64Sum(t, metrics, "ocman.llm.tokens.cache_read", 80)
	assertInt64Sum(t, metrics, "ocman.llm.tokens.cache_write", 20)
	assertFloat64Sum(t, metrics, "ocman.llm.cost", 0.015)

	// Verify histogram has exactly 1 data point (only the first record
	// had a non-zero duration).
	if h, ok := metrics["ocman.llm.request.duration"]; ok {
		hist, ok := h.Data.(metricdata.Histogram[float64])
		if !ok {
			t.Fatalf("duration: unexpected data type %T", h.Data)
		}
		var totalCount uint64
		for _, dp := range hist.DataPoints {
			totalCount += dp.Count
		}
		if totalCount != 1 {
			t.Errorf("duration histogram count = %d, want 1", totalCount)
		}
	}
}

func TestLLMMetricsLoopDisabled(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	shutdown, err := telemetry.Init(t.Context(), "", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	m := newFixture(t).Module
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	m.runLLMMetricsLoop(ctx)
	if ctx.Err() != nil {
		t.Fatal("disabled telemetry kept the LLM scanner running until cancellation")
	}
}

// The scanner feeds external dashboards, so it never waits for a visible
// ocman page: the module has no access to browser demand at all.
func TestLLMMetricsTick(t *testing.T) {
	m := newTestModule(Deps{})
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	t.Cleanup(func() { provider.Shutdown(context.Background()) })
	metrics, err := newLLMMetrics(provider.Meter("test"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	m.getNewAssistantMessages = func(_ context.Context, since int64) ([]db.LLMMessageRow, int64, error) {
		calls++
		if since == 2 {
			return nil, since, nil
		}
		return []db.LLMMessageRow{{Model: "test", SessionID: "session", OutputTokens: 50}}, 2, nil
	}
	hwm := int64(1)
	m.runLLMMetricsTick(context.Background(), metrics, &hwm)
	m.runLLMMetricsTick(context.Background(), metrics, &hwm)
	if calls != 2 || hwm != 2 {
		t.Fatalf("calls=%d hwm=%d, want 2 and 2", calls, hwm)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	collected := make(map[string]metricdata.Metrics)
	for _, scope := range rm.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			collected[instrument.Name] = instrument
		}
	}
	assertInt64Sum(t, collected, "ocman.llm.requests", 1)
	assertInt64Sum(t, collected, "ocman.llm.tokens.output", 50)

	// A failed scan keeps the high-water mark.
	m.getNewAssistantMessages = func(context.Context, int64) ([]db.LLMMessageRow, int64, error) {
		return nil, 99, errors.New("scan failed")
	}
	m.runLLMMetricsTick(context.Background(), metrics, &hwm)
	if hwm != 2 {
		t.Fatalf("hwm after a failed scan = %d, want 2", hwm)
	}
}

// assertInt64Sum checks that the sum of all data points for a counter equals want.
func assertInt64Sum(t *testing.T, metrics map[string]metricdata.Metrics, name string, want int64) {
	t.Helper()
	m, ok := metrics[name]
	if !ok {
		return // already reported as missing
	}
	sum, ok := m.Data.(metricdata.Sum[int64])
	if !ok {
		t.Errorf("%s: unexpected data type %T", name, m.Data)
		return
	}
	var total int64
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	if total != want {
		t.Errorf("%s = %d, want %d", name, total, want)
	}
}

// assertFloat64Sum checks that the sum of all data points for a counter equals want.
func assertFloat64Sum(t *testing.T, metrics map[string]metricdata.Metrics, name string, want float64) {
	t.Helper()
	m, ok := metrics[name]
	if !ok {
		return
	}
	sum, ok := m.Data.(metricdata.Sum[float64])
	if !ok {
		t.Errorf("%s: unexpected data type %T", name, m.Data)
		return
	}
	var total float64
	for _, dp := range sum.DataPoints {
		total += dp.Value
	}
	// Allow small floating-point tolerance.
	if total < want-0.001 || total > want+0.001 {
		t.Errorf("%s = %f, want %f", name, total, want)
	}
}
