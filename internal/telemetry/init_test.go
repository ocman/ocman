package telemetry

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

// TestInitNoEndpointNoOp verifies that an empty endpoint leaves
// telemetry disabled and returns a working no-op shutdown.
func TestInitNoEndpointNoOp(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	hooksBefore := len(log.StandardLogger().Hooks[log.InfoLevel])

	shutdown, err := Init(context.Background(), "", "test")
	if err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("Init returned nil shutdown")
	}
	if Enabled() {
		t.Fatal("empty endpoint enabled telemetry")
	}
	if got := len(log.StandardLogger().Hooks[log.InfoLevel]); got != hooksBefore {
		t.Fatal("disabled telemetry installed a logging hook")
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
	if Enabled() {
		t.Fatal("invalid endpoint enabled telemetry")
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
	if !Enabled() {
		t.Fatal("successful initialization did not enable telemetry")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if Enabled() {
		t.Fatal("shutdown left telemetry enabled")
	}
}

func TestInitEnabledFromEnvironment(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	shutdown, err := Init(t.Context(), "", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if !Enabled() {
		t.Fatal("environment endpoint did not enable telemetry")
	}
}

func TestInitGRPCEndpointFallsBackToHTTP(t *testing.T) {
	var hits atomic.Int32
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/traces" {
			hits.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(collector.Close)
	logger := log.StandardLogger()
	oldHooks := logger.ReplaceHooks(make(log.LevelHooks))
	oldOutput := logger.Out
	var console bytes.Buffer
	logger.SetOutput(&console)
	t.Cleanup(func() {
		logger.ReplaceHooks(oldHooks)
		logger.SetOutput(oldOutput)
	})
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "grpc")

	shutdown, err := Init(t.Context(), "grpc://"+strings.TrimPrefix(collector.URL, "http://"), "test")
	if err != nil {
		t.Fatal(err)
	}
	_, span := otel.Tracer("test").Start(t.Context(), "fallback")
	span.End()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if hits.Load() == 0 {
		t.Fatal("gRPC endpoint did not export over OTLP/HTTP")
	}
	if got := strings.Count(console.String(), "OTLP/gRPC is not supported"); got != 2 {
		t.Fatalf("want endpoint and protocol warnings, got %d in %q", got, console.String())
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

func TestBuildResource_DefaultsVersionToDev(t *testing.T) {
	res, err := buildResource(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := res.Set().Value("service.version"); !ok || v.AsString() != "dev" {
		t.Fatalf("service.version = %v, want dev", v.AsString())
	}
}
