package telemetry

import (
	"testing"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestUIUsageSecondsCounter(t *testing.T) {
	reader := withTestMeterProvider(t)
	RecordUIUsage(t.Context(), 0)
	RecordUIUsage(t.Context(), 1500)
	RecordUIUsage(t.Context(), 2500)
	got := collectMetrics(t, reader)["ocman.ui.active_time"]
	sum, ok := got.Data.(metricdata.Sum[float64])
	if !ok || got.Unit != "s" || !sum.IsMonotonic || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 4 || sum.DataPoints[0].Attributes.Len() != 0 {
		t.Fatalf("usage metric = %+v", got)
	}
}
