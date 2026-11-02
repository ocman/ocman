package statev2

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

// The dashboard hour rows must sum to the same per-agent/finish totals as the
// facts they summarize, both on live ingestion and after upgrading a store
// that still has the pre-dashboard analytics_hour shape.
func TestAnalyticsHourDashboardColumnsMatchFactsAcrossUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Messages = []Message{
		{ID: "a", Role: "assistant", CreatedAt: 3600000, Extensions: json.RawMessage(`{"role":"assistant","agent":"build","providerID":"p","modelID":"m","cost":0.5,"tokens":{"input":10,"output":20,"cache":{"read":3,"write":1}},"time":{"created":1000,"completed":5000},"finish":"stop"}`)},
		{ID: "b", Role: "assistant", CreatedAt: 3600001, Extensions: json.RawMessage(`{"role":"assistant","agent":"build","providerID":"p","modelID":"m","tokens":{"input":7,"output":9},"finish":"stop"}`)},
		{ID: "c", Role: "assistant", CreatedAt: 3600002, Extensions: json.RawMessage(`{"role":"assistant","agent":"plan","providerID":"p","modelID":"m","error":{"name":"APIError"},"tokens":{"input":1,"output":2}}`)},
		{ID: "u", Role: "user", CreatedAt: 3600003, Extensions: json.RawMessage(`{"role":"user"}`)},
	}
	b.Parts = nil
	commit(t, d, b)
	const query = `SELECT agent,finish,messages,completed,errors,successes,duration_ms,timed,throughput,
		priced_cost,unpriced_input,unpriced_output,cache_efficiency FROM analytics_hour WHERE role='assistant' ORDER BY agent,finish`
	type hour struct {
		agent, finish                          string
		messages, completed, errors, successes int
		duration, timed, throughput            int64
		priced                                 float64
		unpricedIn, unpricedOut                int64
		cacheEfficiency                        float64
	}
	read := func() []hour {
		t.Helper()
		rows, err := d.db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var got []hour
		for rows.Next() {
			var h hour
			if err := rows.Scan(&h.agent, &h.finish, &h.messages, &h.completed, &h.errors, &h.successes, &h.duration, &h.timed, &h.throughput,
				&h.priced, &h.unpricedIn, &h.unpricedOut, &h.cacheEfficiency); err != nil {
				t.Fatal(err)
			}
			got = append(got, h)
		}
		return got
	}
	want := []hour{
		{agent: "build", finish: "stop", messages: 2, completed: 2, successes: 2, duration: 4000, timed: 1, throughput: 1, priced: 0.5, unpricedIn: 7, unpricedOut: 9, cacheEfficiency: 0.75},
		{agent: "plan", finish: "", messages: 1, completed: 1, errors: 1, unpricedIn: 1, unpricedOut: 2},
	}
	check := func(stage string) {
		t.Helper()
		got := read()
		if len(got) != len(want) {
			t.Fatalf("%s: hour rows = %+v, want %+v", stage, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: hour row %d = %+v, want %+v", stage, i, got[i], want[i])
			}
		}
	}
	check("ingest")
	// Restore the previous analytics_hour shape and version, then upgrade.
	if _, err := d.db.Exec(`DROP TABLE analytics_hour;
		CREATE TABLE analytics_hour (
		 instance_id TEXT NOT NULL, session_id TEXT NOT NULL, bucket INTEGER NOT NULL,
		 role TEXT NOT NULL, provider TEXT NOT NULL, model TEXT NOT NULL, valid INTEGER NOT NULL,
		 first_at INTEGER NOT NULL, last_at INTEGER NOT NULL, messages INTEGER NOT NULL,
		 input_tokens INTEGER NOT NULL, output_tokens INTEGER NOT NULL,
		 cache_read INTEGER NOT NULL, cache_write INTEGER NOT NULL, cost REAL NOT NULL,
		 PRIMARY KEY(instance_id,session_id,bucket,role,provider,model,valid),
		 FOREIGN KEY(instance_id,session_id) REFERENCES session(instance_id,id) ON DELETE CASCADE
		) WITHOUT ROWID;
		CREATE INDEX analytics_hour_window ON analytics_hour(instance_id,role,bucket);
		PRAGMA user_version=` + fmt.Sprint(migrationIndex(t, analyticsDashboardSchema))); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	check("upgrade")
}
