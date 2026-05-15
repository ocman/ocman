package telemetry

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/uptrace/opentelemetry-go-extra/otellogrus"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ShutdownFunc flushes both providers and releases their resources.
// Safe to call exactly once; subsequent calls are no-ops.
type ShutdownFunc func(context.Context) error

// noop is the shutdown returned when telemetry is disabled.
var noop ShutdownFunc = func(context.Context) error { return nil }

// Init wires up the OTel SDK against endpoint and registers the
// resulting providers as the OTel globals. version is folded into
// the resource as service.version (overridden if the operator sets
// OTEL_RESOURCE_ATTRIBUTES=service.version=...).
//
// endpoint may be:
//   - ""                            -> telemetry disabled, returns no-op.
//   - "http://host:4318"            -> OTLP/HTTP.
//   - "https://host:4318"           -> OTLP/HTTP over TLS.
//   - "grpc://host:4317"            -> OTLP/gRPC (insecure).
//   - "grpcs://host:4317"           -> OTLP/gRPC (TLS).
//   - "host:4317" (no scheme)       -> OTLP/gRPC (insecure).
//
// If endpoint is empty but OTEL_EXPORTER_OTLP_ENDPOINT is set,
// the env var wins and telemetry is enabled.
func Init(ctx context.Context, endpoint, version string) (ShutdownFunc, error) {
	if endpoint == "" {
		endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	}
	if endpoint == "" {
		return noop, nil
	}

	target, err := parseEndpoint(endpoint)
	if err != nil {
		return noop, fmt.Errorf("invalid --otel endpoint %q: %w", endpoint, err)
	}

	// buildResource may return a partial resource alongside an error
	// (e.g. one detector tripped). A partial resource is still usable;
	// log and proceed instead of disabling telemetry entirely.
	res, err := buildResource(ctx, version)
	if err != nil {
		log.WithError(err).Warn("OTel resource detection partial; continuing")
	}

	traceExp, err := newTraceExporter(ctx, target)
	if err != nil {
		return noop, fmt.Errorf("creating trace exporter: %w", err)
	}
	metricExp, err := newMetricExporter(ctx, target)
	if err != nil {
		// Best-effort cleanup of the trace exporter we already built.
		_ = traceExp.Shutdown(ctx)
		return noop, fmt.Errorf("creating metric exporter: %w", err)
	}

	// Trace provider: BatchSpanProcessor is the production default;
	// it batches spans before export to amortise the network cost.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExp),
		sdktrace.WithResource(res),
	)

	// Metric provider: PeriodicReader drives metric collection at a
	// fixed interval. The OTel default is 60s; ocman uses 30s so a
	// dashboard that pulls fresh metrics every minute always sees a
	// recent point. OTEL_METRIC_EXPORT_INTERVAL overrides this.
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExp,
			sdkmetric.WithInterval(30*time.Second),
		)),
		sdkmetric.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	// W3C trace context + baggage are the conventional defaults.
	// Ocman doesn't propagate baggage anywhere meaningful yet but
	// installing it here means future cross-process work just works.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	// Auto-collect Go runtime metrics (heap, goroutines, GC pauses).
	// Best-effort: a runtime-instrumentation failure shouldn't tank
	// the whole telemetry init.
	if err := runtime.Start(runtime.WithMeterProvider(mp), runtime.WithMinimumReadMemStatsInterval(15*time.Second)); err != nil {
		log.WithError(err).Warn("OTel runtime instrumentation failed to start")
	}

	// Bridge logrus -> OTel logs (via span events). Once installed,
	// every logrus call inside an active span gets surfaced as a
	// span event with the level and message attached, and the log
	// line itself is decorated with trace_id/span_id so external
	// tooling (Loki, etc.) can correlate them.
	log.AddHook(otellogrus.NewHook(otellogrus.WithLevels(
		log.PanicLevel,
		log.FatalLevel,
		log.ErrorLevel,
		log.WarnLevel,
		log.InfoLevel,
	)))

	log.WithFields(log.Fields{
		"endpoint": endpoint,
		"protocol": target.protocol,
		"insecure": target.insecure,
	}).Info("OTel telemetry initialised")

	shutdown := func(ctx context.Context) error {
		var errs []error
		if err := tp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("trace provider shutdown: %w", err))
		}
		if err := mp.Shutdown(ctx); err != nil {
			errs = append(errs, fmt.Errorf("metric provider shutdown: %w", err))
		}
		return errors.Join(errs...)
	}
	return shutdown, nil
}

// buildResource folds ocman's service identity into the OTel resource.
//
// We don't pin a schema URL on our local attribute set: that lets the
// resource keep whatever SchemaURL the SDK's Default() picked, which
// in turn moves with otel/sdk releases. Pinning it (e.g. to
// semconv/v1.26.0) would make Merge() error on every SDK upgrade with
// "conflicting Schema URL". The attribute keys we set
// (service.name, service.version) are stable across schema versions,
// so dropping the URL has no observable effect on the data.
//
// service.instance.id is a per-process UUID. Without it, two ocman
// processes pushing to the same OTLP endpoint (e.g. the Air-built dev
// binary and a separately-installed GUI build) collide on a single
// Prometheus series — the counters interleave, every push looks like
// a counter reset to rate(), and dashboards report rates orders of
// magnitude higher than reality. Setting it forces each process into
// its own series and the dashboards become accurate again.
//
// OTEL_SERVICE_NAME / OTEL_RESOURCE_ATTRIBUTES still win because
// resource.WithFromEnv runs last and Merge prefers the latter.
func buildResource(ctx context.Context, version string) (*resource.Resource, error) {
	if version == "" {
		version = "dev"
	}
	// Order matters: resource.New folds the option list left-to-right
	// and later detectors win on conflict. We want OTEL_SERVICE_NAME
	// / OTEL_RESOURCE_ATTRIBUTES to override our defaults, so the
	// env-detector runs last.
	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", "ocman"),
			attribute.String("service.version", version),
			attribute.String("service.instance.id", uuid.NewString()),
		),
		resource.WithTelemetrySDK(),
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
		resource.WithProcessRuntimeDescription(),
		resource.WithHost(),
		resource.WithFromEnv(),
	)
	if err != nil {
		// resource.New returns a partial resource alongside the error
		// (e.g. when one detector fails). The partial value is still
		// usable; callers should log and proceed.
		return res, err
	}
	return res, nil
}
