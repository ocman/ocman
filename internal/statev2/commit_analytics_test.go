package statev2

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestPartialCommitLeavesUnrelatedAnalyticsUntouched(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	old := b.Messages[0]
	old.ID, old.Position, old.CreatedAt = "old", 1, 7200000
	b.Messages = append(b.Messages, old)
	b.Parts = append(b.Parts, Part{ID: "old-question", MessageID: "old", Kind: "tool", Extensions: json.RawMessage(`{"tool":"question","state":{"time":{"start":7200001,"end":7200009}}}`)})
	revision := commit(t, d, b).Revision
	for _, trigger := range []string{
		`CREATE TRIGGER keep_fact BEFORE DELETE ON analytics_fact WHEN OLD.id='old' BEGIN SELECT RAISE(ABORT,'unrelated fact rebuilt'); END`,
		`CREATE TRIGGER keep_question BEFORE DELETE ON analytics_question WHEN OLD.message_id='old' BEGIN SELECT RAISE(ABORT,'unrelated question rebuilt'); END`,
		`CREATE TRIGGER keep_hour BEFORE DELETE ON analytics_hour WHEN OLD.bucket=7200000 BEGIN SELECT RAISE(ABORT,'unrelated hour rebuilt'); END`,
	} {
		if _, err := d.db.Exec(trigger); err != nil {
			t.Fatal(err)
		}
	}
	update := b.Messages[0]
	update.Extensions = json.RawMessage(`{"role":"assistant","tokens":{"input":7,"output":9}}`)
	if _, err := d.Commit(t.Context(), Batch{Session: b.Session, ExpectedRevision: revision, Messages: []Message{update}}); err != nil {
		t.Fatalf("partial commit rebuilt unrelated history: %v", err)
	}
	var in, out int64
	if err := d.db.QueryRow(`SELECT input_tokens,output_tokens FROM analytics_session`).Scan(&in, &out); err != nil || in != 7 || out != 9 {
		t.Fatalf("session totals = %d %d, %v", in, out, err)
	}
}

func TestCompleteCommitRebuildsAnalytics(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	extra := b.Messages[0]
	extra.ID, extra.Position = "dropped", 1
	b.Messages = append(b.Messages, extra)
	revision := commit(t, d, b).Revision
	// An authoritative snapshot that omits a message must remove its fact.
	b.Messages, b.Complete, b.ExpectedRevision = b.Messages[:1], true, revision
	commit(t, d, b)
	var n int
	if err := d.db.QueryRow(`SELECT count(*) FROM analytics_fact WHERE id='dropped'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("omitted message facts = %d, %v", n, err)
	}
}

// Partial commits refresh only the messages they touch. The result must equal
// a full rebuild across appends, edits that move hour buckets, part-only
// changes and metadata-only batches.
func TestPartialCommitAnalyticsMatchRebuild(t *testing.T) {
	d := openTestDB(t)
	s := Session{Key: Key{InstanceID: "first", SessionID: "history"}, Title: "history", Directory: "/repo", CreatedAt: 1, UpdatedAt: 2, Extensions: json.RawMessage(`{}`)}
	at := func(i int) int64 { return int64(i) * 1_700_000 } // spans many hour buckets
	message := func(i int) Message {
		role := "assistant"
		if i%4 == 0 {
			role = "user"
		}
		return Message{ID: fmt.Sprintf("m%03d", i), Position: int64(i), Role: role, CreatedAt: at(i), CompletedAt: at(i) + 5000, ConversationVisible: true,
			Extensions: json.RawMessage(fmt.Sprintf(`{"role":%q,"finish":"stop","modelID":"m%d","providerID":"p","time":{"created":%d,"completed":%d},"tokens":{"input":%d,"output":%d,"cache":{"read":3,"write":4}},"cost":0.5}`,
				role, i%3, at(i), at(i)+5000, i, i*2))}
	}
	tool := func(i int, name string, end int64) Part {
		return Part{ID: fmt.Sprintf("m%03d-tool", i), MessageID: fmt.Sprintf("m%03d", i), Position: 1, Kind: "tool",
			Extensions: json.RawMessage(fmt.Sprintf(`{"tool":%q,"state":{"status":"completed","time":{"start":%d,"end":%d}}}`, name, at(i)+100, end))}
	}
	base := Batch{Session: s, Complete: true}
	for i := range 40 {
		base.Messages = append(base.Messages, message(i))
		base.Parts = append(base.Parts, tool(i, "bash", at(i)+900))
	}
	revision := commit(t, d, base).Revision
	apply := func(b Batch) {
		t.Helper()
		b.Session, b.ExpectedRevision = s, revision
		revision = commit(t, d, b).Revision
	}
	for i := 40; i < 60; i++ {
		apply(Batch{Messages: []Message{message(i)}, Parts: []Part{tool(i, "bash", at(i)+900)}})
	}
	edited := message(5)
	edited.CreatedAt += 7_200_000
	edited.Extensions = json.RawMessage(`{"role":"assistant","finish":"error","tokens":{"input":999,"output":1},"cost":3}`)
	apply(Batch{Messages: []Message{edited}})
	apply(Batch{Parts: []Part{tool(7, "question", at(7)+90000)}})
	s.Title = "renamed"
	apply(Batch{})

	tables := []string{"analytics_fact", "analytics_hour", "analytics_question", "analytics_session"}
	got := map[string][]string{}
	for _, table := range tables {
		got[table] = tableRows(t, d.db.Query, table)
	}
	tx, err := d.db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := rebuildAnalyticsSummary(t.Context(), tx, s.Key); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		want := tableRows(t, tx.Query, table)
		if len(want) == 0 {
			t.Fatalf("%s: rebuild produced no rows", table)
		}
		if strings.Join(got[table], "\n") != strings.Join(want, "\n") {
			t.Errorf("%s after partial commits:\n%s\nfull rebuild:\n%s", table, strings.Join(got[table], "\n"), strings.Join(want, "\n"))
		}
	}
}

func tableRows(t *testing.T, query func(string, ...any) (*sql.Rows, error), table string) []string {
	t.Helper()
	rows, err := query("SELECT * FROM " + table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(columns))
		targets := make([]any, len(columns))
		for i := range values {
			targets[i] = &values[i]
		}
		if err := rows.Scan(targets...); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprint(values...))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}
