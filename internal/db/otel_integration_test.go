package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSQLiteSpansParentUnderRequestSpan(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	opencodePath := filepath.Join(t.TempDir(), "opencode.db")
	seed, err := sql.Open("sqlite", opencodePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.Exec(`CREATE TABLE probe (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	opencodeDB, err := Open(opencodePath)
	if err != nil {
		t.Fatal(err)
	}
	defer opencodeDB.Close()

	ctx, requestSpan := otel.Tracer("test").Start(t.Context(), "GET /test", trace.WithSpanKind(trace.SpanKindServer))
	var one int
	if err := opencodeDB.db.QueryRowContext(ctx, `SELECT 1`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	requestSpan.End()

	requestID := requestSpan.SpanContext().SpanID()
	found := map[string]bool{}
	for _, span := range exporter.GetSpans() {
		name := attributeValue(span.Attributes, "db.name")
		if name != "opencode" && name != "ocman" {
			continue
		}
		if span.Parent.SpanID() == requestID {
			found[name] = true
		}
	}
	if !found["opencode"] {
		t.Error("no opencode DB span recorded")
	}
}

func attributeValue(attrs []attribute.KeyValue, key string) string {
	for _, attr := range attrs {
		if string(attr.Key) == key {
			return attr.Value.AsString()
		}
	}
	return ""
}
