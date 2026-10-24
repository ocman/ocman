package statev2

import (
	"encoding/json"
	"testing"
)

func TestAnalyticsSummariesFollowAtomicSnapshotsAndToolOnlyUpdates(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Complete = true
	b.Messages[0].Extensions = json.RawMessage(`{"role":"assistant","agent":"build","providerID":"p","modelID":"m","tokens":{"input":10,"output":20},"time":{"created":1000,"completed":11000},"finish":"stop"}`)
	b.Parts = []Part{{ID: "tool", MessageID: "message", Kind: "tool", Extensions: json.RawMessage(`{"tool":"question","state":{"time":{"start":2000,"end":4000}}}`)}}
	commit(t, d, b)
	check := func(duration, tools, count int64) {
		t.Helper()
		var gotDuration, gotTools, gotCount int64
		if err := d.db.QueryRow(`SELECT agent_ms,tool_ms FROM analytics_fact`).Scan(&gotDuration, &gotTools); err != nil {
			t.Fatal(err)
		}
		if err := d.db.QueryRow(`SELECT sum(messages) FROM analytics_hour`).Scan(&gotCount); err != nil {
			t.Fatal(err)
		}
		if gotDuration != duration || gotTools != tools || gotCount != count {
			t.Fatalf("summaries = %d %d %d, want %d %d %d", gotDuration, gotTools, gotCount, duration, tools, count)
		}
	}
	check(8000, 2000, 1)
	if result := commit(t, d, b); !result.Duplicate {
		t.Fatal("duplicate checkpoint not recognized")
	}
	check(8000, 2000, 1)
	b.Complete = false
	b.ExpectedRevision = 1
	b.Checkpoint = nil
	b.Messages = nil
	b.Parts[0].Extensions = json.RawMessage(`{"tool":"question","state":{"time":{"start":2000,"end":6000}}}`)
	commit(t, d, b)
	check(6000, 4000, 1)
	// A failure after summary writes must roll back both summaries and history.
	if _, err := d.db.Exec(`CREATE TRIGGER fail_summary_notification BEFORE INSERT ON change_outbox BEGIN SELECT RAISE(ABORT,'test failure'); END`); err != nil {
		t.Fatal(err)
	}
	b.ExpectedRevision = 2
	b.Parts[0].SourceDeleted = true
	if _, err := d.Commit(t.Context(), b); err == nil {
		t.Fatal("expected rollback")
	}
	check(6000, 4000, 1)
	if _, err := d.db.Exec(`DROP TRIGGER fail_summary_notification`); err != nil {
		t.Fatal(err)
	}
	b.Complete = true
	b.Messages = nil
	b.Parts = nil
	commit(t, d, b)
	for _, table := range []string{"analytics_fact", "analytics_hour", "analytics_question"} {
		var count int
		if err := d.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("complete snapshot left %s rows: %d %v", table, count, err)
		}
	}
}
