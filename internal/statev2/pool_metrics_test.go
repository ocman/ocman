package statev2

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestConversationPoolMetricsIncludeWaitsAndUnregisterOnClose(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	defer func() { otel.SetMeterProvider(previous); _ = provider.Shutdown(context.Background()) }()
	d, err := Open(t.TempDir() + "/conversation.db")
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	conn, err := d.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if _, err := d.db.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("writer did not wait")
	}
	_ = conn.Close()
	collect := func() metricdata.ResourceMetrics {
		var result metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	found := map[string]bool{}
	wait := false
	waitDuration := false
	for _, scope := range collect().ScopeMetrics {
		for _, m := range scope.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[float64]); ok {
				for _, p := range sum.DataPoints {
					name, _ := p.Attributes.Value("db.name")
					if m.Name == "db.sql.connection.wait_duration" && name.AsString() == "statev2" && p.Value > 0 {
						waitDuration = true
					}
				}
			}
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, p := range sum.DataPoints {
					name, _ := p.Attributes.Value("db.name")
					if name.AsString() == "statev2" || name.AsString() == "statev2-read" {
						found[name.AsString()] = true
					}
					if m.Name == "db.sql.connection.wait" && name.AsString() == "statev2" && p.Value > 0 {
						wait = true
					}
				}
			}
		}
	}
	if len(found) != 2 || !wait || !waitDuration {
		t.Fatalf("pool metrics=%v wait=%v duration=%v, collected=%+v", found, wait, waitDuration, collect())
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, scope := range collect().ScopeMetrics {
		for _, m := range scope.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok {
				for _, p := range sum.DataPoints {
					name, _ := p.Attributes.Value("db.name")
					if name.AsString() == "statev2" || name.AsString() == "statev2-read" {
						t.Fatalf("closed pool still observed: %s", m.Name)
					}
				}
			}
		}
	}
}
