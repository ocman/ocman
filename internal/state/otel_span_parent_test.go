package state

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// TestSQLiteSpansParentUnderRequestSpan is the ocman half of the check that
// lives in internal/db for the opencode handle; the two halves are split
// because internal/db tests cannot import this package (state depends on
// platforms, which depends on db).
func TestSQLiteSpansParentUnderRequestSpan(t *testing.T) {
	exporter := recordStateSQL(t)
	d := openTestStateDB(t)

	ctx, requestSpan := otel.Tracer("test").Start(t.Context(), "GET /test", trace.WithSpanKind(trace.SpanKindServer))
	if _, _, err := d.GetSetting(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
	requestSpan.End()

	requestID := requestSpan.SpanContext().SpanID()
	found := false
	for _, span := range exporter.GetSpans() {
		if span.Parent.SpanID() != requestID {
			continue
		}
		for _, attr := range span.Attributes {
			if attr.Key == "db.name" && attr.Value.AsString() == "ocman" {
				found = true
			}
		}
	}
	if !found {
		t.Error("no ocman DB span recorded under the request span")
	}
}
