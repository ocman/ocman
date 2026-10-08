package forgehttp_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/NoUseFreak/ocman/internal/forge"
	"github.com/NoUseFreak/ocman/internal/forge/forgehttp"
	"github.com/NoUseFreak/ocman/internal/forge/forgejo"
	"github.com/NoUseFreak/ocman/internal/forge/github"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestGetEmitsClientSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	defer func() { _ = provider.Shutdown(context.Background()) }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }))
	defer srv.Close()
	ctx, parent := provider.Tracer("test").Start(context.Background(), "request")
	defer parent.End()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/repos/o/r/pulls", nil)
	client := srv.Client()
	client.Timeout = time.Second
	instrumented := forgehttp.InstrumentClient(client)
	if instrumented.Timeout != client.Timeout || instrumented.Transport == client.Transport {
		t.Fatal("client settings not preserved or transport not wrapped")
	}
	if _, _, _, err := forgehttp.Get(ctx, instrumented, req); err != nil {
		t.Fatal(err)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].SpanKind != trace.SpanKindClient || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("client spans=%v", spans)
	}
}

func TestForgeClientsEmitSpans(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	defer otel.SetTracerProvider(previous)
	defer func() { _ = provider.Shutdown(context.Background()) }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"login":"alice"}`)) }))
	defer srv.Close()
	clients := []forge.Forge{
		github.NewForTest(srv.URL, "test", srv.Client()),
		forgejo.NewForTest("forge.test", srv.URL, "test", srv.Client()),
	}
	for _, client := range clients {
		exporter.Reset()
		ctx, parent := provider.Tracer("test").Start(context.Background(), "request")
		if _, err := client.CurrentUser(ctx); err != nil {
			t.Fatal(err)
		}
		spans := exporter.GetSpans()
		if len(spans) != 1 || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() || spans[0].SpanKind != trace.SpanKindClient {
			t.Fatalf("%s spans=%v", client.Host(), spans)
		}
		parent.End()
	}
}
