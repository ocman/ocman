package statev2

import (
	"strings"
	"testing"
)

func accountedBatch(instance string) Batch {
	b := fixture(instance)
	b.Session.ParentID = "parent"
	b.Session.ProjectID = "project"
	b.Messages = []Message{
		{ID: "a", Position: 0, Role: "assistant", Provider: "anthropic", Model: "anthropic/claude", CreatedAt: 100, CompletedAt: 110,
			Cost: 0.25, TokensInput: 10, TokensOutput: 4, TokensReasoning: 1, TokensCacheRead: 2, TokensCacheWrite: 3,
			Finish: "stop", HasError: false},
		{ID: "b", Position: 1, Role: "assistant", Provider: "openai", Model: "openai/gpt", CreatedAt: 200, CompletedAt: 210,
			Cost: 0.5, TokensInput: 7, TokensOutput: 3, Finish: "error", HasError: true, ErrorName: "Boom"},
	}
	b.Parts = nil
	return b
}

func TestUsageAggregatesStoredAccounting(t *testing.T) {
	d := openTestDB(t)
	b := accountedBatch("first")
	commit(t, d, b)
	got, err := d.Usage(t.Context(), b.Session.Key)
	if err != nil {
		t.Fatal(err)
	}
	want := Usage{Messages: 2, Cost: 0.75, TokensInput: 17, TokensOutput: 7, TokensReasoning: 1, TokensCacheRead: 2, TokensCacheWrite: 3}
	if got != want {
		t.Fatalf("usage = %+v, want %+v", got, want)
	}
	if empty, err := d.Usage(t.Context(), Key{InstanceID: "first", SessionID: "missing"}); err != nil || empty != (Usage{}) {
		t.Fatalf("missing session usage: %+v %v", empty, err)
	}
}

func TestUsageExcludesSourceDeletedMessages(t *testing.T) {
	d := openTestDB(t)
	b := accountedBatch("first")
	commit(t, d, b)
	b.Messages = b.Messages[:1]
	b.Complete = true
	b.ExpectedRevision = 1
	b.Checkpoint = &CheckpointUpdate{Stream: "runtime/generation", ExpectedCursor: "1", Cursor: "2"}
	commit(t, d, b)
	got, err := d.Usage(t.Context(), b.Session.Key)
	if err != nil || got.Messages != 1 || got.Cost != 0.25 {
		t.Fatalf("removed message still counted: %+v %v", got, err)
	}
}

func TestAccountingColumnsPersistFromBatch(t *testing.T) {
	d := openTestDB(t)
	b := accountedBatch("first")
	commit(t, d, b)
	detail := read(t, d, b.Session.Key)
	if detail.Session.ParentID != "parent" || detail.Session.ProjectID != "project" {
		t.Fatalf("session identity columns: %+v", detail.Session)
	}
	m := detail.Messages[0]
	if m.Provider != "anthropic" || m.Finish != "stop" || m.TokensCacheWrite != 3 || m.Cost != 0.25 {
		t.Fatalf("message accounting: %+v", m)
	}
	if e := detail.Messages[1]; !e.HasError || e.ErrorName != "Boom" {
		t.Fatalf("message error: %+v", e)
	}
}

// planUses reports whether SQLite answers query through the named index
// without scanning the whole table.
func planUses(t *testing.T, d *DB, query, index string, args ...any) {
	t.Helper()
	rows, err := d.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	joined := strings.Join(plan, " | ")
	if !strings.Contains(joined, index) || strings.Contains(joined, "SCAN message") || strings.Contains(joined, "SCAN session") {
		t.Fatalf("query %q plan %q does not use %s", query, joined, index)
	}
}

func TestAnalyticsQueriesUseIndexes(t *testing.T) {
	d := openTestDB(t)
	planUses(t, d, `SELECT count(*) FROM message WHERE instance_id=? AND role='assistant' AND created_at>=?`, "message_role", "first", int64(0))
	planUses(t, d, `SELECT count(*) FROM session WHERE instance_id=? AND created_at>=?`, "session_created", "first", int64(0))
	planUses(t, d, `SELECT count(*) FROM message WHERE created_at >= ?`, "message_created", int64(0))
	planUses(t, d, `SELECT count(*) FROM message WHERE instance_id=? AND model=? AND created_at >= ?`, "message_model", "first", "x", int64(0))
	planUses(t, d, `SELECT id FROM session WHERE instance_id=? AND parent_id=?`, "session_parent", "first", "parent")
	planUses(t, d, `SELECT id FROM session WHERE instance_id=? AND project_id=?`, "session_project", "first", "project")
}
