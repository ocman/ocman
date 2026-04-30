package telemetry

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// instrumentationName is the scope shared by every ocman-issued span
// and metric. Scope names are how OTel back-ends group telemetry by
// the library that produced it; using one constant keeps ocman's
// telemetry findable in a noisy collector.
const instrumentationName = "github.com/NoUseFreak/ocman"

// Tracer returns the ocman-scoped tracer. When telemetry is disabled,
// the global TracerProvider is the SDK no-op and this tracer's spans
// are zero-cost.
func Tracer() trace.Tracer {
	return otel.Tracer(instrumentationName)
}

// Meter returns the ocman-scoped meter. Same no-op semantics as
// Tracer when telemetry is disabled.
func Meter() metric.Meter {
	return otel.Meter(instrumentationName)
}
