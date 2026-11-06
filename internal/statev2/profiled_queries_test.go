package statev2

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Capture the statements actually executed, not a second copy of their SQL.
func recordQueries(t *testing.T) *tracetest.InMemoryExporter {
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

func queryPlan(t *testing.T, d *DB, query string) string {
	t.Helper()
	args := make([]any, strings.Count(query, "?"))
	rows, err := d.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(plan, "\n")
}

func TestSummaryFactLookupsAreSessionBounded(t *testing.T) {
	exporter := recordQueries(t)
	d := openTestDB(t)
	commit(t, d, summaryBatch("first", "root", ""))
	for _, scope := range []struct{ directory, id string }{{"", "root"}, {"/repo", "root"}, {"/repo", ""}, {"", ""}} {
		exporter.Reset()
		if _, err := d.SessionSummaries(t.Context(), "first", scope.directory, scope.id); err != nil {
			t.Fatal(err)
		}
		checked := 0
		for _, span := range exporter.GetSpans() {
			for _, attr := range span.Attributes {
				query := attr.Value.AsString()
				if string(attr.Key) != "db.query.text" || !strings.Contains(query, "FROM analytics_fact") && !strings.Contains(query, "JOIN analytics_fact") {
					continue
				}
				checked++
				plan := queryPlan(t, d, query)
				for _, line := range strings.Split(plan, "\n") {
					if (strings.Contains(line, "analytics_fact") || strings.Contains(line, "SEARCH f ") || strings.Contains(line, "SCAN f")) && !strings.Contains(line, "session_id=?") {
						t.Errorf("scope %+v has source-wide fact lookup: %s\n%s", scope, line, plan)
					}
				}
			}
		}
		if checked != 2 {
			t.Fatalf("expected timings and models statements, got %d", checked)
		}
	}
}

func TestConversationTreeUsesPointAndParentLookups(t *testing.T) {
	exporter := recordQueries(t)
	d := openTestDB(t)
	commit(t, d, summaryBatch("first", "root", ""))
	commit(t, d, summaryBatch("first", "child", "root"))
	exporter.Reset()
	if _, err := d.ReadConversation(t.Context(), Key{"first", "child"}, 30, 0); err != nil {
		t.Fatal(err)
	}
	for _, span := range exporter.GetSpans() {
		for _, attr := range span.Attributes {
			query := attr.Value.AsString()
			if string(attr.Key) != "db.query.text" || !strings.Contains(query, "RECURSIVE tree") {
				continue
			}
			plan := queryPlan(t, d, query)
			if strings.Contains(plan, "session_created") || !strings.Contains(plan, "parent_id=?") || !strings.Contains(plan, "id=?") {
				t.Fatalf("tree scans unrelated sessions:\n%s", plan)
			}
			for _, line := range strings.Split(plan, "\n") {
				if (strings.Contains(line, "SEARCH session ") || strings.Contains(line, "SEARCH s ") || strings.Contains(line, "SCAN session ") || strings.Contains(line, "SCAN s ")) &&
					!strings.Contains(line, "(instance_id=? AND id=?)") && !strings.Contains(line, "(instance_id=? AND parent_id=?)") {
					t.Fatalf("tree lookup is not bounded to the frontier: %s", line)
				}
			}
			return
		}
	}
	t.Fatal("tree query was not captured")
}

// An isolated representative history, never a copy of a live database.
func TestProfiledQueriesAtHistoryScale(t *testing.T) {
	d := openTestDB(t)
	commit(t, d, summaryBatch("first", "root", ""))
	for i := range 12 {
		commit(t, d, summaryBatch("first", string(rune('a'+i)), "root"))
	}
	// Bulk fixtures omit ingestion work intentionally: only the queried tables
	// need unrelated rows to reproduce the planner's source-wide scan.
	_, err := d.db.Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<20000)
		INSERT INTO session(instance_id,id,title,directory,created_at,updated_at,source_deleted,revision,extensions,parent_id,project_id,read_format,is_subagent)
		SELECT 'first','unrelated-'||x,'unrelated','/elsewhere',1,1,0,1,'{}','','',1,0 FROM n;
		WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<32)
		INSERT INTO analytics_fact SELECT s.instance_id,s.id,'fact-'||n.x,1,'assistant','p','model','',0,7,3,0,0,1,2,1,0,0,0,0,'stop',0,1,1
		FROM session s CROSS JOIN n WHERE s.id LIKE 'unrelated-%'`)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	rows, err := d.SessionSummaries(t.Context(), "first", "", "root")
	if err != nil || len(rows) != 1 || len(rows[0].Totals.Models) != 1 {
		t.Fatalf("summary: %+v %v", rows, err)
	}
	t.Logf("20,013 sessions / 640,026 facts: single summary %s", time.Since(start))
	start = time.Now()
	page, err := d.ReadConversation(t.Context(), Key{"first", "root"}, 30, 0)
	if err != nil || len(page.Tree) != 13 {
		t.Fatalf("family: %+v %v", page, err)
	}
	t.Logf("20,013 sessions: 13-node conversation family %s", time.Since(start))
}
