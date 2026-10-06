package telemetry

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	_ "modernc.org/sqlite"
)

func TestNormalizeSQL(t *testing.T) {
	for _, tt := range []struct{ query, want string }{
		{"SELECT id FROM session WHERE id = ?", "select id from session where id = ?"},
		{" select\nID from session where id=?42 -- comment\n", "select id from session where id = ?"},
		{"SELECT /* comment */ id FROM session WHERE id=:session", "select id from session where id = ?"},
		{"SELECT id FROM session WHERE id=@session", "select id from session where id = ?"},
		{"SELECT id FROM session WHERE id=$session", "select id from session where id = ?"},
		{"SELECT id FROM session WHERE id='secret''value'", "select id from session where id = ?"},
		{"SELECT X'CAFE', 0xAB, 123, .5, 1.25e-3, 1e+2", "select ? , ? , ? , ? , ? , ?"},
		{"SELECT id FROM session WHERE id IN (?, ?, ?)", "select id from session where id in ( ? )"},
		{"SELECT id FROM session WHERE id IN ('a', 'b')", "select id from session where id in ( ? )"},
		{"SELECT id FROM session WHERE id IN (?)", "select id from session where id in ( ? )"},
		{"SELECT \"time_created\", `id`, [title] FROM session", "select \"time_created\" , `id` , [title] from session"},
		{"SELECT \"a\"\"b\", `a``b` FROM session", "select \"a\"\"b\" , `a``b` from session"},
		{"SELECT column1, table2.id FROM table2", "select column1 , table2 . id from table2"},
		{"SELECT 1-2, 3+4", "select ? - ? , ? + ?"},
		{"-- nothing", ""},
		{"/* unfinished", ""},
		{"SELECT 'unfinished", "select ?"},
		{"SELECT \"unfinished", "select \"unfinished"},
		{"SELECT id /* unfinished", "select id"},
		{"", ""},
	} {
		t.Run(tt.query, func(t *testing.T) {
			if got := normalizeSQL(tt.query); got != tt.want {
				t.Fatalf("normalizeSQL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSQLQueryAttributes(t *testing.T) {
	ctx := t.Context()
	attrs := attribute.NewSet(sqlQueryAttributes(ctx, otelsql.MethodConnQuery, "SELECT id FROM session WHERE id=?", nil)...)
	fingerprint, _ := attrs.Value("db.query.fingerprint")
	if len(fingerprint.AsString()) != 16 {
		t.Fatalf("fingerprint = %q", fingerprint.AsString())
	}
	// Bound argument values are never inspected or exported.
	same := attribute.NewSet(sqlQueryAttributes(ctx, otelsql.MethodStmtQuery, "select id from session where id='secret'", []driver.NamedValue{{Value: "another secret"}})...)
	if !attrs.Equals(&same) {
		t.Fatalf("equivalent query attributes differ: %v / %v", attrs, same)
	}
	for _, query := range []string{"SELECT title FROM session WHERE id=?", "SELECT id FROM message WHERE id=?", "SELECT id FROM session WHERE id!=?"} {
		other := attribute.NewSet(sqlQueryAttributes(ctx, otelsql.MethodConnQuery, query, nil)...)
		value, _ := other.Value("db.query.fingerprint")
		if value == fingerprint {
			t.Errorf("different query has same fingerprint: %s", query)
		}
	}
	if got := sqlQueryAttributes(ctx, otelsql.MethodTxCommit, "", nil); got != nil {
		t.Fatalf("non-query operation has attributes: %v", got)
	}
	long := attribute.NewSet(sqlQueryAttributes(ctx, otelsql.MethodConnQuery, "SELECT "+strings.Repeat("column, ", 100)+"id FROM session", nil)...)
	summary, _ := long.Value("db.query.summary")
	if len([]rune(summary.AsString())) != 161 || !strings.HasSuffix(summary.AsString(), "…") {
		t.Fatalf("summary not truncated: %q", summary.AsString())
	}
}

func TestSQLMetricsAndSpans(t *testing.T) {
	reader := withTestMeterProvider(t)
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	db, err := OpenSQL(":memory:", "probe")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(t.Context(), "CREATE TABLE probe (id INTEGER)"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO probe VALUES (?)", 1); err != nil {
		t.Fatal(err)
	}
	stmt, err := db.PrepareContext(t.Context(), "INSERT INTO probe VALUES (?)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.ExecContext(t.Context(), 2); err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	stmt, err = db.PrepareContext(t.Context(), "SELECT id FROM probe WHERE id=?")
	if err != nil {
		t.Fatal(err)
	}
	var id int
	if err := stmt.QueryRowContext(t.Context(), 1).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRowContext(t.Context(), "SELECT id FROM probe WHERE id=?", 2).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), "INSERT INTO missing VALUES (?)", 3); err == nil {
		t.Fatal("expected SQL error")
	}
	histogram := collectMetrics(t, reader)["db.sql.latency"].Data.(metricdata.Histogram[float64])
	wanted := map[string]string{
		"sql.conn.exec":  "insert into probe values ( ? )",
		"sql.stmt.exec":  "insert into probe values ( ? )",
		"sql.conn.query": "select id from probe where id = ?",
		"sql.stmt.query": "select id from probe where id = ?",
	}
	found := map[string]bool{}
	failed := false
	for _, dp := range histogram.DataPoints {
		method, _ := dp.Attributes.Value("method")
		summary, _ := dp.Attributes.Value("db.query.summary")
		fingerprint, _ := dp.Attributes.Value("db.query.fingerprint")
		if expected, ok := wanted[method.AsString()]; ok && summary.AsString() == expected {
			if fingerprint.AsString() == "" || dp.Count != 1 {
				t.Errorf("missing fingerprint or incorrect execution count: %v", dp)
			}
			found[method.AsString()] = true
		}
		status, _ := dp.Attributes.Value("status")
		if summary.AsString() == "insert into missing values ( ? )" && status.AsString() == "error" {
			failed = true
		}
	}
	if len(found) != len(wanted) || !failed {
		t.Fatalf("missing execution metrics: found %v, failed=%v; points=%v", found, failed, histogram.DataPoints)
	}
	for _, span := range exporter.GetSpans() {
		if span.Name == "sql.stmt.query" {
			attrs := attribute.NewSet(span.Attributes...)
			if _, ok := attrs.Value("db.query.fingerprint"); !ok {
				t.Fatal("prepared query span missing fingerprint")
			}
			return
		}
	}
	t.Fatal("no prepared query span")
}

func BenchmarkSQLQueryAttributes(b *testing.B) {
	query := "SELECT id, title FROM session WHERE id IN (" + strings.Repeat("?,", 99) + "?) AND time_updated > ?"
	for b.Loop() {
		sqlQueryAttributes(b.Context(), otelsql.MethodConnQuery, query, nil)
	}
}
