package telemetry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// TestInitNoEndpointNoOp verifies that an empty endpoint leaves
// telemetry disabled and returns a working no-op shutdown.
func TestInitNoEndpointNoOp(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")

	shutdown, err := Init(context.Background(), "", "test")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

// TestInitInvalidEndpoint surfaces parser errors instead of silently
// disabling telemetry — operators who typo the endpoint should see
// the failure on boot.
func TestInitInvalidEndpoint(t *testing.T) {
	_, err := Init(context.Background(), "tcp://nope", "test")
	if err == nil {
		t.Fatal("expected error for invalid scheme, got nil")
	}
}

func TestInitHTTPExporterAndShutdown(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer collector.Close()

	shutdown, err := Init(t.Context(), collector.URL, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestGRPCExportersConstructAndShutdown(t *testing.T) {
	otlpTarget := target{protocol: protoGRPC, endpoint: "127.0.0.1:1", insecure: true}
	traceExporter, err := newTraceExporter(t.Context(), otlpTarget)
	if err != nil {
		t.Fatal(err)
	}
	metricExporter, err := newMetricExporter(t.Context(), otlpTarget)
	if err != nil {
		t.Fatal(err)
	}
	if err := traceExporter.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := metricExporter.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := newTraceExporter(t.Context(), target{}); err == nil {
		t.Fatal("trace exporter accepted an unknown protocol")
	}
	if _, err := newMetricExporter(t.Context(), target{}); err == nil {
		t.Fatal("metric exporter accepted an unknown protocol")
	}
}

// TestBuildResource_SetsUniqueInstanceID verifies that every call to
// buildResource produces a distinct service.instance.id. Two ocman
// processes pushing to the same OTLP endpoint without this attribute
// collide on a single Prometheus series, which makes rate() lie by
// orders of magnitude because every interleaved sample looks like a
// counter reset.
func TestBuildResource_SetsUniqueInstanceID(t *testing.T) {
	r1, err := buildResource(context.Background(), "test")
	if err != nil {
		t.Fatalf("buildResource (1): %v", err)
	}
	r2, err := buildResource(context.Background(), "test")
	if err != nil {
		t.Fatalf("buildResource (2): %v", err)
	}

	id1, ok1 := lookupAttr(r1.Attributes(), "service.instance.id")
	id2, ok2 := lookupAttr(r2.Attributes(), "service.instance.id")
	if !ok1 || !ok2 {
		t.Fatalf("service.instance.id missing: r1=%v r2=%v", ok1, ok2)
	}
	if id1 == "" || id2 == "" {
		t.Fatalf("service.instance.id empty: %q %q", id1, id2)
	}
	if id1 == id2 {
		t.Fatalf("service.instance.id collided across calls: %q", id1)
	}
}

func lookupAttr(attrs []attribute.KeyValue, key string) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

// TestTracerMeterNoOpWithoutInit ensures that callers can fetch the
// ocman tracer/meter even when Init has never been called. The OTel
// SDK's default no-op providers must keep the call sites cheap.
func TestTracerMeterNoOpWithoutInit(t *testing.T) {
	tracer := Tracer()
	if tracer == nil {
		t.Fatal("Tracer() returned nil")
	}
	_, span := tracer.Start(context.Background(), "noop")
	span.End()

	meter := Meter()
	if meter == nil {
		t.Fatal("Meter() returned nil")
	}
}
