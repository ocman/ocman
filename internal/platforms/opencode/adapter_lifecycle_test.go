package opencode

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/NoUseFreak/ocman/internal/db"
	"github.com/NoUseFreak/ocman/internal/platforms"
)

func TestSessionLifecycleReadsOneMetadataQuery(t *testing.T) {
	withTestPort(t, "/repo", "7777")
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	a := New(newLifecycleFixtureDB(t, 10000, false), nil)
	exporter.Reset() // Exclude schema detection and setup reads.
	if _, err := a.SessionLifecycle(t.Context(), "s"); err != nil {
		t.Fatal(err)
	}
	queries := 0
	for _, span := range exporter.GetSpans() {
		if span.Name != "sql.conn.query" {
			continue
		}
		queries++
		var statement string
		for _, attr := range span.Attributes {
			if string(attr.Key) == "db.statement" {
				statement = attr.Value.AsString()
			}
		}
		if !strings.Contains(statement, "LIMIT 1") || strings.Contains(statement, "part") || strings.Contains(statement, "parent_id") {
			t.Fatalf("unexpected lifecycle query: %s", statement)
		}
	}
	if queries != 1 {
		t.Fatalf("lifecycle read made %d DB queries, want one metadata query", queries)
	}
}

func TestSessionLifecycleSettlesLatestMessage(t *testing.T) {
	withTestPort(t, "/repo", "7777")
	database := newTestDBWithSessions(t, []testSession{
		{id: "s", directory: "/repo", messages: []string{`{"role":"user"}`, `{"role":"assistant","finish":"stop"}`}},
		{id: "open", directory: "/repo", messages: []string{`{"role":"assistant"}`}},
		{id: "gone", directory: "/elsewhere", messages: []string{`{"role":"assistant"}`}},
		{id: "empty", directory: "/repo"},
	})
	a := New(database, nil)
	a.ObserveSessionStatus("7777", 0, "s", "idle")
	a.ObserveSessionStatus("7777", 0, "open", "busy")

	got, err := a.SessionLifecycle(t.Context(), "s")
	want := platforms.SessionLifecycle{Status: db.StatusWaiting, LatestMessageID: "msg-s-1", LatestMessageCreated: 1001, LatestMessageRole: "assistant"}
	if err != nil || *got != want {
		t.Fatalf("settled = %+v, %v; want %+v", got, err, want)
	}
	if got, err := a.SessionLifecycle(t.Context(), "open"); err != nil || got.Status != db.StatusBusy {
		t.Fatalf("running = %+v, %v; want busy", got, err)
	}
	// No instance serves it, so an unfinished turn can never finish.
	if got, err := a.SessionLifecycle(t.Context(), "gone"); err != nil || got.Status != db.StatusInterrupted {
		t.Fatalf("unowned = %+v, %v; want interrupted", got, err)
	}
	if got, err := a.SessionLifecycle(t.Context(), "empty"); err != nil || *got != (platforms.SessionLifecycle{Status: db.StatusDone}) {
		t.Fatalf("empty = %+v, %v; want done without a message", got, err)
	}
	if _, err := a.SessionLifecycle(t.Context(), "missing"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("missing err = %v, want ErrNotFound", err)
	}
	if _, err := (&Adapter{}).SessionLifecycle(t.Context(), "s"); !errors.Is(err, platforms.ErrNotFound) {
		t.Fatalf("no db err = %v, want ErrNotFound", err)
	}
}

// TestSessionLifecycleBoundedByTranscript proves the queue's lifecycle read
// does not scale with the transcript: historical messages are malformed JSON
// (any parse of them fails the query), the part table does not exist (any
// part read fails), the session has a subagent tree, and allocations and
// payload stay the same at 10, 1,000 and 10,000 messages.
func TestSessionLifecycleBoundedByTranscript(t *testing.T) {
	withTestPort(t, "/repo", "7777")
	var allocs []float64
	var payloads []int
	for _, n := range []int{10, 1000, 10000} {
		a := New(newLifecycleFixtureDB(t, n, false), nil)
		a.ObserveSessionStatus("7777", 0, "s", "idle")
		var got *platforms.SessionLifecycle
		allocs = append(allocs, testing.AllocsPerRun(5, func() {
			var err error
			if got, err = a.SessionLifecycle(t.Context(), "s"); err != nil {
				t.Fatalf("%d messages: %v", n, err)
			}
		}))
		if want := fmt.Sprintf("m%06d", n-1); got.LatestMessageID != want || got.Status != db.StatusWaiting {
			t.Fatalf("%d messages: got %+v, want latest %s waiting", n, got, want)
		}
		b, _ := json.Marshal(got)
		payloads = append(payloads, len(b))
	}
	t.Logf("allocs per read at 10/1k/10k messages: %v; payload bytes: %v", allocs, payloads)
	// Only the timestamp's digit count may change the payload.
	for i := 1; i < len(allocs); i++ {
		if allocs[i] > allocs[0]+5 || payloads[i] > payloads[0]+8 {
			t.Fatalf("lifecycle read grows with transcript: allocs %v, payload %v", allocs, payloads)
		}
	}
}

// BenchmarkSessionQueueRead compares the queue's old per-decision read,
// Session(id, 1, 0), with SessionLifecycle on the DB path.
func BenchmarkSessionQueueRead(b *testing.B) {
	restore := setDiscoverPortsImplForTests(func() map[string]string { return map[string]string{} })
	resetPortCacheForTests()
	b.Cleanup(func() { restore(); resetPortCacheForTests() })
	for _, n := range []int{10, 1000, 10000} {
		a := New(newLifecycleFixtureDB(b, n, true), nil)
		b.Run(fmt.Sprintf("Session/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := a.Session(b.Context(), "s", 1, 0); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(fmt.Sprintf("SessionLifecycle/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := a.SessionLifecycle(b.Context(), "s"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// newLifecycleFixtureDB seeds session "s" (with two subagent children) and
// n messages in a temp DB. valid=false writes malformed history and no part
// table; valid=true writes parseable history with one part per message.
func newLifecycleFixtureDB(tb testing.TB, n int, valid bool) *db.DB {
	tb.Helper()
	path := tb.TempDir() + "/opencode.db"
	setup, err := sql.Open("sqlite", path)
	if err != nil {
		tb.Fatal(err)
	}
	history := `'not json'`
	if valid {
		history = `'{"role":"assistant","finish":"stop","padding":"' || printf('%.*c', 512, 'x') || '"}'`
	}
	stmts := []string{
		`CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT NOT NULL DEFAULT '', parent_id TEXT,
			title TEXT NOT NULL DEFAULT '', directory TEXT NOT NULL DEFAULT '', time_created INTEGER NOT NULL DEFAULT 0,
			time_updated INTEGER NOT NULL DEFAULT 0, summary_additions INTEGER, summary_deletions INTEGER,
			summary_files INTEGER, share_url TEXT)`,
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL DEFAULT 0,
			data TEXT NOT NULL DEFAULT '{}')`,
		`CREATE INDEX message_session_idx ON message(session_id, time_created, id)`,
		`INSERT INTO session (id, parent_id, directory, time_created, time_updated) VALUES
			('s', NULL, '/repo', 1, 2), ('c1', 's', '/repo', 1, 2), ('c2', 's', '/repo', 1, 2)`,
		fmt.Sprintf(`WITH RECURSIVE n(i) AS (SELECT 0 UNION ALL SELECT i+1 FROM n WHERE i < %d)
			INSERT INTO message SELECT printf('m%%06d', i), 's', 1000+i,
			CASE WHEN i = %d THEN '{"role":"assistant","finish":"stop"}' ELSE %s END FROM n`, n-1, n-1, history),
	}
	if valid {
		stmts = append(stmts,
			`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL,
				time_created INTEGER NOT NULL DEFAULT 0, data TEXT NOT NULL DEFAULT '{}')`,
			`INSERT INTO part SELECT 'p' || id, id, session_id, time_created, '{"type":"text","text":"hello"}' FROM message`)
	}
	for _, stmt := range stmts {
		if _, err := setup.Exec(stmt); err != nil {
			setup.Close()
			tb.Fatalf("seeding: %v\n%s", err, stmt)
		}
	}
	setup.Close()
	database, err := db.OpenReadWrite(path)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { database.Close() })
	return database
}
