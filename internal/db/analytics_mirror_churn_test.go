package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestAnalyticsMirrorUnchangedSyncDoesNotRewriteRows(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		_ = provider.Shutdown(context.Background())
	})
	d, _ := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	// An unfinished turn makes the refresh window include settled history.
	insertMessage(t, d, "running", "s2", now-2*time.Hour.Milliseconds(), map[string]any{"role": "assistant"})
	insertMessage(t, d, "recent", "s2", now, map[string]any{"role": "assistant", "finish": "stop"})
	if _, err := d.db.Exec(`INSERT INTO part (id, message_id, session_id, data)
		VALUES ('tool', 'recent', 's2', '{"type":"tool","state":{"time":{"start":1,"end":2}}}'),
		('numeric', 'recent', 's2', '{"type":"tool","state":{"time":2}}'),
		('boolean', 'recent', 's2', '{"type":"tool","state":{"time":true}}'),
		('string', 'recent', 's2', '{"type":"tool","state":{"time":"10"}}'),
		('null', 'recent', 's2', '{"type":"tool","state":{"time":null}}')`); err != nil {
		t.Fatal(err)
	}
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	count := func() uint64 {
		t.Helper()
		var data metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &data); err != nil {
			t.Fatal(err)
		}
		var count uint64
		for _, scope := range data.ScopeMetrics {
			for _, metric := range scope.Metrics {
				if metric.Name != "db.sql.latency" {
					continue
				}
				for _, point := range metric.Data.(metricdata.Histogram[float64]).DataPoints {
					database, _ := point.Attributes.Value("db.name")
					method, _ := point.Attributes.Value("method")
					if database.AsString() == "analytics-cache" && method.AsString() == "sql.stmt.exec" {
						count += point.Count
					}
				}
			}
		}
		return count
	}
	before := count()
	if before == 0 {
		t.Fatal("initial copy recorded no row executions")
	}
	forceStale(d)
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	if writes := count() - before; writes != 0 {
		t.Fatalf("unchanged incremental sync executed %d row writes, want 0", writes)
	}
}

func TestReconcileMirrorRowsErrors(t *testing.T) {
	for _, test := range []struct {
		name, query, existing, insert, remove, trigger string
		n                                              int
	}{
		{name: "mirror query", existing: "SELECT missing FROM session"},
		{name: "source query", query: "SELECT missing FROM session"},
		{name: "scan", n: 4},
		{name: "prepare insert", insert: "INSERT INTO missing VALUES (?)"},
		{name: "prepare delete", remove: "DELETE FROM missing WHERE id = ?"},
		{name: "delete changed", trigger: "CREATE TRIGGER reject BEFORE DELETE ON session BEGIN SELECT RAISE(ABORT, 'blocked'); END"},
		{name: "insert", trigger: "CREATE TRIGGER reject BEFORE INSERT ON session BEGIN SELECT RAISE(ABORT, 'blocked'); END"},
		{name: "delete missing", query: "SELECT id, parent_id, directory, title, time_created FROM session WHERE 0", trigger: "CREATE TRIGGER reject BEFORE DELETE ON session BEGIN SELECT RAISE(ABORT, 'blocked'); END"},
	} {
		t.Run(test.name, func(t *testing.T) {
			d, _ := openMirrored(t)
			seedMirrorSource(t, d, time.Now().UnixMilli())
			if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
				t.Fatal(err)
			}
			if _, err := d.db.Exec(`UPDATE session SET title = 'Changed'`); err != nil {
				t.Fatal(err)
			}
			if test.trigger != "" {
				if _, err := d.mirror.db.Exec(test.trigger); err != nil {
					t.Fatal(err)
				}
			}
			query := `SELECT id, parent_id, directory, title, time_created FROM session`
			existing := query
			insert := `INSERT INTO session (id, parent_id, directory, title, time_created) VALUES (?, ?, ?, ?, ?)`
			remove := `DELETE FROM session WHERE id = ?`
			n := 5
			if test.query != "" {
				query = test.query
			}
			if test.existing != "" {
				existing = test.existing
			}
			if test.insert != "" {
				insert = test.insert
			}
			if test.remove != "" {
				remove = test.remove
			}
			if test.n != 0 {
				n = test.n
			}
			tx, err := d.mirror.db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := reconcileMirrorRows(t.Context(), d.db, tx, query, nil, existing, insert, remove, n); err == nil {
				t.Fatal("expected reconciliation error")
			}
		})
	}
}

func TestAnalyticsMirrorDeltaKeepsParity(t *testing.T) {
	d, _ := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	insertMessage(t, d, "running", "s2", now-5*time.Minute.Milliseconds(), map[string]any{"role": "assistant"})
	insertMessage(t, d, "recent", "s2", now, map[string]any{"role": "assistant", "finish": "stop"})
	insertPart(t, d, "tool-a", "running", "s2", now, map[string]any{"type": "tool"})
	insertPart(t, d, "tool-b", "running", "s2", now, map[string]any{"type": "tool"})
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{
		`UPDATE session SET title = 'Renamed', directory = '/p/c', parent_id = 's1' WHERE id = 's2'`,
		`UPDATE message SET data = '{"role":"assistant","finish":"stop","tokens":{"output":11}}' WHERE id = 'running'`,
		`UPDATE part SET data = '{"type":"tool","state":{"time":{"start":1,"end":2}}}' WHERE id = 'tool-a'`,
		`INSERT INTO session (id, title, directory, time_created) VALUES ('s3', 'New', '/p/new', 1)`,
		`DELETE FROM part WHERE id = 'tool-b'`,
		`DELETE FROM message WHERE id = 'running'`,
		`DELETE FROM session WHERE id = 's1'; DELETE FROM message WHERE session_id = 's1'`,
	} {
		t.Run(change, func(t *testing.T) {
			if _, err := d.db.Exec(change); err != nil {
				t.Fatal(err)
			}
			forceStale(d)
			if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
				t.Fatal(err)
			}
			mirror := d.mirror
			d.mirror = nil
			want := takeAnalytics(t, d, now)
			d.mirror = mirror
			if got := takeAnalytics(t, d, now); !reflect.DeepEqual(got, want) {
				t.Fatalf("delta sync differs from source:\n got %+v\nwant %+v", got, want)
			}
		})
	}
	var timings int
	if err := d.mirror.db.QueryRow(`SELECT count(*) FROM tool_timing`).Scan(&timings); err != nil {
		t.Fatal(err)
	}
	if timings != 0 {
		t.Fatalf("deleted message left %d tool timings", timings)
	}
}

func TestAnalyticsMirrorDeltaRollsBack(t *testing.T) {
	d, _ := openMirrored(t)
	now := time.Now().UnixMilli()
	seedMirrorSource(t, d, now)
	if err := d.SyncAnalyticsMirror(t.Context()); err != nil {
		t.Fatal(err)
	}
	insertMessage(t, d, "new", "s2", now, map[string]any{"role": "user"})
	if _, err := d.db.Exec(`UPDATE session SET title = 'Changed' WHERE id = 's2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := d.mirror.db.Exec(`CREATE TRIGGER reject_session BEFORE INSERT ON session
		BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	forceStale(d)
	if err := d.SyncAnalyticsMirror(t.Context()); err == nil {
		t.Fatal("expected session insert failure")
	}
	// Inspect the committed mirror without retrying the failed refresh.
	if ids := mirrorIDs(t, d); len(ids) != 4 {
		t.Fatalf("failed sync committed messages: %v", ids)
	}
	var title string
	if err := d.mirror.db.QueryRow(`SELECT title FROM session WHERE id = 's2'`).Scan(&title); err != nil {
		t.Fatal(err)
	}
	if title != "Helper (@explore subagent)" {
		t.Fatalf("failed sync changed session: %q", title)
	}
}
