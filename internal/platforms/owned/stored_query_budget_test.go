package owned

import (
	"context"
	"fmt"
	"testing"

	"github.com/NoUseFreak/ocman/internal/db"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStoredBrowsingBatchesCatalogLookups(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	a, p, id := storedFixture(t)
	a.native = &budgetProjector{nativePlatform: p}
	baseline := map[string]int{}
	for _, count := range []int{1, 31} {
		if count > 1 {
			for i := range count - 1 {
				publish(t, a, db.Session{ID: fmt.Sprintf("other-%d", i), Directory: "/other", Title: "other"})
			}
		}
		for name, read := range map[string]func() error{
			"list":         func() error { _, err := a.Sessions(t.Context(), "", 0); return err },
			"conversation": func() error { _, err := a.Session(t.Context(), id, 30, 0); return err },
		} {
			t.Run(fmt.Sprintf("%s/%d", name, count), func(t *testing.T) {
				exporter.Reset()
				if err := read(); err != nil {
					t.Fatal(err)
				}
				queries := 0
				for _, span := range exporter.GetSpans() {
					if span.Name == "sql.conn.query" {
						queries++
					}
				}
				if count == 1 {
					baseline[name] = queries
				} else if queries != baseline[name] {
					t.Fatalf("%s query count grew from %d to %d when inventory grew from 1 to %d sessions", name, baseline[name], queries, count)
				}
				t.Logf("%s: %d queries for %d sessions", name, queries, count)
			})
		}
	}
}

type budgetProjector struct{ *nativePlatform }

func (p *budgetProjector) EnrichStoredSessions(context.Context, []db.Session, map[string]string) error {
	return nil
}
