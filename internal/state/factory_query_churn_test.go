package state

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func recordStateSQL(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	return exporter
}

func TestFactoryEpicProjectsAreBatched(t *testing.T) {
	exporter := recordStateSQL(t)
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	formula := nativeTracerFormula(t)
	for _, goal := range []string{"First", "Second", "Third"} {
		if _, err := d.CreateFactoryEpic(t.Context(), "", goal, "", "/repo", "", formula); err != nil {
			t.Fatal(err)
		}
	}
	exporter.Reset()
	epics, err := d.ListFactoryEpics(t.Context())
	if err != nil || len(epics) != 3 {
		t.Fatalf("epics = %v, %v", epics, err)
	}
	queries := 0
	for _, span := range exporter.GetSpans() {
		if span.Name == "sql.conn.query" {
			queries++
		}
	}
	if queries != 2 {
		t.Fatalf("listing 3 epics executed %d queries, want 2", queries)
	}
	for _, epic := range epics {
		if len(epic.Projects) != 1 || epic.Projects[0].Path != "/repo" || epic.Projects[0].Removable {
			t.Fatalf("projects = %#v", epic.Projects)
		}
	}
}

func TestFactoryWorkflowDoesNotRewriteUnchangedPhase(t *testing.T) {
	d := openTestStateDB(t)
	t.Cleanup(func() { _ = d.Close() })
	ep, err := d.CreateFactoryEpic(t.Context(), "", "Unchanged phase", "", "/repo", "", nativeTracerFormula(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO factory_issue(id, epic_id, project_path, kind, title, status, created_at) VALUES ('phase', ?, '/repo', 'phase', 'Phase', 'open', 1)`, ep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.db.Exec(`INSERT INTO factory_workflow_step(issue_id, definition_json) VALUES ('phase', '{"key":"phase","kind":"implementation"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reconcileFactoryWorkflow(t.Context(), ep.ID); err != nil {
		t.Fatal(err)
	}
	var before, after int
	if err := d.db.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := d.reconcileFactoryWorkflow(t.Context(), ep.ID); err != nil {
		t.Fatal(err)
	}
	if err := d.db.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("unchanged workflow rewrote %d rows", after-before)
	}
}
