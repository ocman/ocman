package telemetry

import (
	"context"
	"testing"
	"time"
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
