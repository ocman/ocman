package conversationingest

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/ocman/ocman/internal/db"
	"github.com/ocman/ocman/internal/platforms"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// writeUnits counts committed writes on the statev2 writer handle: each
// transaction counts once, as does each statement executed outside one.
func writeUnits(spans tracetest.SpanStubs) int {
	slices.SortFunc(spans, func(a, b tracetest.SpanStub) int { return a.StartTime.Compare(b.StartTime) })
	units, inTx := 0, false
	for _, span := range spans {
		if !slices.Contains(span.Attributes, attribute.String("db.name", "statev2")) && !strings.HasPrefix(span.Name, "sql.tx.") {
			continue
		}
		switch span.Name {
		case "sql.conn.begin_tx":
			inTx = true
			units++
		case "sql.tx.commit", "sql.tx.rollback":
			inTx = false
		case "sql.conn.exec":
			if !inTx {
				units++
			}
		}
	}
	return units
}

// An imported session's read receipt, version hint and content commit
// together: one write transaction instead of one per bookkeeping key.
func TestImportCommitsSourceStateWithContent(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); _ = provider.Shutdown(context.Background()) })

	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	run := func() int {
		t.Helper()
		exporter.Reset()
		if err := w.execute(t.Context(), pass{reads: []db.HistoryEntry{{ID: "session", Version: "v1"}}}, nil); err != nil {
			t.Fatal(err)
		}
		return writeUnits(exporter.GetSpans())
	}
	if units := run(); units != 1 {
		t.Fatalf("new session import used %d write transactions; want 1", units)
	}
	state, err := store.SourceState(t.Context(), sourceKey("one").InstanceID)
	if err != nil || state["read-ready/session"] != "v1" || state[versionKey+"session"] != "v1" {
		t.Fatalf("source state after import = %v, %v", state, err)
	}
	if units := run(); units != 1 {
		t.Fatalf("unchanged session check used %d write transactions; want 1", units)
	}
}

// Without a pre-import clear, a session the source no longer has must still
// lose the receipt and hint committed by its earlier import, and must not
// gain new ones while being retained as source-deleted.
func TestSourceDeletedImportWithdrawsReceiptAndHint(t *testing.T) {
	f := fakeSource("one")
	m, store := testImporter(t, f)
	w := m.workers[0]
	if err := w.ensureInstance(t.Context()); err != nil {
		t.Fatal(err)
	}
	entry := db.HistoryEntry{ID: "session", Version: "v1"}
	if err := w.execute(t.Context(), pass{reads: []db.HistoryEntry{entry}}, nil); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.entries, f.snapshotErr = nil, platforms.ErrNotFound
	f.mu.Unlock()
	result, err := w.importSession(t.Context(), entry)
	if err != nil || result.outcome != "source-deleted" {
		t.Fatalf("import of a vanished session = %+v, %v", result, err)
	}
	state, err := store.SourceState(t.Context(), sourceKey("one").InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, hint := state["read-ready/session"], state[versionKey+"session"]; receipt != "" || hint != "" {
		t.Fatalf("source-deleted session kept receipt %q and hint %q", receipt, hint)
	}
	if _, hinted := w.versions["session"]; hinted {
		t.Fatal("source-deleted session kept its in-memory hint")
	}
	if s, err := store.SessionState(t.Context(), sourceKey("one")); err != nil || !s.SourceDeleted {
		t.Fatalf("vanished session not retained as source-deleted: %+v %v", s, err)
	}
	if text, _ := importText(t, store, sourceKey("one")); text != "one" {
		t.Fatalf("source deletion discarded retained content: %q", text)
	}
}
