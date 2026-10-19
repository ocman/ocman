package statev2

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIncrementalSummaryLeavesUnrelatedHistoryUntouched(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	old := b.Messages[0]
	old.ID, old.Position, old.CreatedAt = "old", 1, 7200000
	b.Messages = append(b.Messages, old)
	b.Parts = append(b.Parts, Part{ID: "old-question", MessageID: "old", Kind: "tool", Extensions: json.RawMessage(`{"tool":"question","state":{"time":{"start":7200001,"end":7200009}}}`)})
	commit(t, d, b)
	for _, trigger := range []string{
		`CREATE TRIGGER keep_fact BEFORE DELETE ON analytics_fact WHEN OLD.id='old' BEGIN SELECT RAISE(ABORT,'unrelated fact rebuilt'); END`,
		`CREATE TRIGGER keep_question BEFORE DELETE ON analytics_question WHEN OLD.message_id='old' BEGIN SELECT RAISE(ABORT,'unrelated question rebuilt'); END`,
		`CREATE TRIGGER keep_hour BEFORE DELETE ON analytics_hour WHEN OLD.bucket=7200000 BEGIN SELECT RAISE(ABORT,'unrelated hour rebuilt'); END`,
	} {
		if _, err := d.db.Exec(trigger); err != nil {
			t.Fatal(err)
		}
	}
	b.Messages[0].Extensions = json.RawMessage(`{"role":"assistant","tokens":{"input":7,"output":9}}`)
	_, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: b.Session.InstanceID, Checkpoint: CheckpointUpdate{Stream: "live", Cursor: "1"}, Batches: []StreamBatch{{Key: b.Session.Key, Messages: b.Messages[:1]}}})
	if err != nil {
		t.Fatalf("targeted update rebuilt unrelated history: %v", err)
	}
	var in, out int64
	if err := d.db.QueryRow(`SELECT input_tokens,output_tokens FROM analytics_session`).Scan(&in, &out); err != nil || in != 7 || out != 9 {
		t.Fatalf("session totals = %d %d, %v", in, out, err)
	}
}

func TestAnalyticsSubagentUpgradePreservesFacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, d, "first")
	b := fixture("first")
	b.Session.Subagent = true
	commit(t, d, b)
	if _, err := d.db.Exec(fmt.Sprintf(`ALTER TABLE analytics_session DROP COLUMN is_subagent; DROP INDEX analytics_fact_hour; PRAGMA user_version=%d`, len(migrations)-1)); err != nil {
		t.Fatal(err)
	}
	d.Close()
	d, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var child bool
	if err := d.db.QueryRow(`SELECT is_subagent FROM analytics_session`).Scan(&child); err != nil || !child {
		t.Fatalf("subagent flag not migrated: %v %v", child, err)
	}
	if got := read(t, d, b.Session.Key); got.Session.Revision != 1 {
		t.Fatal("upgrade rewrote history")
	}
}

func TestIncrementalSummariesMatchFullRepair(t *testing.T) {
	d := openTestDB(t)
	b := fixture("first")
	b.Checkpoint = nil
	commit(t, d, b)
	message := b.Messages[0]
	message.CreatedAt = 7200010
	message.Extensions = json.RawMessage(`{"role":"assistant","cost":0.25,"tokens":{"input":10,"output":20},"time":{"created":7200010,"completed":7201010},"finish":"stop"}`)
	next := message
	next.ID, next.CreatedAt, next.Position = "next", 10800000, 1
	part := Part{ID: "question", MessageID: message.ID, Kind: "tool", Extensions: json.RawMessage(`{"tool":"question","state":{"time":{"start":7200010,"end":7200200}}}`)}
	metadata := b.Session
	metadata.Title, metadata.Subagent = "new title", true
	updates := []StreamBatch{
		{Key: b.Session.Key, Messages: []Message{message}, Parts: []Part{part}},
		{Key: b.Session.Key, Messages: []Message{next}},
		{Key: b.Session.Key, RemovedParts: []PartRef{{MessageID: message.ID, ID: part.ID}}},
		{Key: b.Session.Key, RemovedMessages: []string{message.ID}},
		{Session: &metadata},
		{Key: b.Session.Key, RemovedMessages: []string{next.ID}},
	}
	read := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for _, table := range []string{"analytics_fact", "analytics_hour", "analytics_question", "analytics_session"} {
			rows, err := d.db.Query(`SELECT * FROM ` + table + ` ORDER BY 1,2,3`)
			if err != nil {
				t.Fatal(err)
			}
			cols, _ := rows.Columns()
			var records [][]any
			for rows.Next() {
				values, pointers := make([]any, len(cols)), make([]any, len(cols))
				for i := range values {
					pointers[i] = &values[i]
				}
				if err := rows.Scan(pointers...); err != nil {
					t.Fatal(err)
				}
				records = append(records, values)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			raw, _ := json.Marshal(records)
			result[table] = string(raw)
		}
		return result
	}
	for i, update := range updates {
		cursor := fmt.Sprint(i + 1)
		prior := ""
		if i > 0 {
			prior = fmt.Sprint(i)
		}
		if _, err := d.ApplyStream(t.Context(), StreamUpdate{InstanceID: b.Session.InstanceID, Checkpoint: CheckpointUpdate{Stream: "live", ExpectedCursor: prior, Cursor: cursor}, Batches: []StreamBatch{update}}); err != nil {
			t.Fatal(err)
		}
		got := read()
		var tx *sql.Tx
		tx, err := d.db.BeginTx(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := rebuildAnalyticsSummary(t.Context(), tx, b.Session.Key); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		if want := read(); !reflect.DeepEqual(got, want) {
			t.Fatalf("update %d diverges from repair:\ngot %v\nwant %v", i, got, want)
		}
	}
}
